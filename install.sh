#!/usr/bin/env bash
set -Eeuo pipefail

DEFAULT_REPOSITORY="vfaddey/xray-monitor"
REPOSITORY="${XRAY_MONITOR_REPO:-$DEFAULT_REPOSITORY}"
VERSION="${XRAY_MONITOR_VERSION:-latest}"
PUBLIC_HOST=""
FORCE=0
SKIP_XRAY=0
SUBSCRIPTIONS=()

usage() {
  cat <<'EOF'
Install xray-monitor and its systemd service.

Usage:
  sudo ./install.sh --subscription URL [options]

Options:
  --subscription URL   Subscription URL; may be repeated (required)
  --public-host HOST   Hostname/IP for the API URL (default: detected public IPv4)
  --repo OWNER/REPO    GitHub repository (default: vfaddey/xray-monitor)
  --version VERSION    Release tag such as v1.0.0 (default: latest)
  --skip-xray          Do not automatically install Xray when it is missing
  --force              Replace an existing xray-monitor installation
  -h, --help           Show this help

Environment equivalents:
  XRAY_MONITOR_REPO, XRAY_MONITOR_VERSION
EOF
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

while (($# > 0)); do
  case "$1" in
    --subscription)
      (($# >= 2)) || die "--subscription requires a value"
      SUBSCRIPTIONS+=("$2")
      shift 2
      ;;
    --public-host)
      (($# >= 2)) || die "--public-host requires a value"
      PUBLIC_HOST="$2"
      shift 2
      ;;
    --repo)
      (($# >= 2)) || die "--repo requires OWNER/REPO"
      REPOSITORY="$2"
      shift 2
      ;;
    --version)
      (($# >= 2)) || die "--version requires a release tag"
      VERSION="$2"
      shift 2
      ;;
    --skip-xray)
      SKIP_XRAY=1
      shift
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

[[ "$(id -u)" == "0" ]] || die "run this script as root (sudo)"
((${#SUBSCRIPTIONS[@]} > 0)) || die "at least one --subscription is required"
[[ "$REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || die "--repo must use OWNER/REPO format"
[[ "$(uname -s)" == "Linux" ]] || die "only Linux is supported"

for command_name in curl sha256sum systemctl install mktemp; do
  command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
done

case "$(uname -m)" in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    die "unsupported architecture: $(uname -m)"
    ;;
esac

TEMP_DIR="$(mktemp -d -t xray-monitor-install.XXXXXXXX)"
cleanup() {
  if [[ -n "${TEMP_DIR:-}" && -d "$TEMP_DIR" ]]; then
    rm -rf -- "$TEMP_DIR"
  fi
}
trap cleanup EXIT

if command -v xray >/dev/null 2>&1; then
  XRAY_BINARY="$(command -v xray)"
else
  ((SKIP_XRAY == 0)) || die "xray is missing and --skip-xray was specified"
  printf 'Installing Xray from the official XTLS installer...\n'
  XRAY_INSTALLER="$TEMP_DIR/xray-install.sh"
  curl --fail --silent --show-error --location \
    "https://github.com/XTLS/Xray-install/raw/main/install-release.sh" \
    --output "$XRAY_INSTALLER"
  bash "$XRAY_INSTALLER" install --without-geodata

  # The monitor starts its own Xray child process with generated configs.
  # Disable only the service created by the fresh installation above.
  systemctl disable --now xray.service >/dev/null 2>&1 || true
  XRAY_BINARY="/usr/local/bin/xray"
fi
[[ -x "$XRAY_BINARY" ]] || die "xray executable was not found after installation: $XRAY_BINARY"

ASSET="xray-monitor-linux-${ARCH}"
if [[ "$VERSION" == "latest" ]]; then
  RELEASE_BASE="https://github.com/${REPOSITORY}/releases/latest/download"
else
  [[ "$VERSION" =~ ^v[0-9A-Za-z._+-]+$ ]] || die "invalid release version: $VERSION"
  RELEASE_BASE="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
fi

printf 'Downloading %s from %s...\n' "$ASSET" "$REPOSITORY"
curl --fail --silent --show-error --location \
  "${RELEASE_BASE}/${ASSET}" \
  --output "$TEMP_DIR/$ASSET"
curl --fail --silent --show-error --location \
  "${RELEASE_BASE}/checksums.txt" \
  --output "$TEMP_DIR/checksums.txt"

EXPECTED_SUM="$(awk -v asset="$ASSET" '$2 == asset {print $1}' "$TEMP_DIR/checksums.txt")"
[[ "$EXPECTED_SUM" =~ ^[0-9a-fA-F]{64}$ ]] || die "release checksum for $ASSET was not found or is invalid"
printf '%s  %s\n' "$EXPECTED_SUM" "$TEMP_DIR/$ASSET" | sha256sum --check --status -
printf 'SHA-256 verified.\n'
chmod 0755 "$TEMP_DIR/$ASSET"

INSTALL_ARGS=(install --xray-binary "$XRAY_BINARY")
for subscription in "${SUBSCRIPTIONS[@]}"; do
  INSTALL_ARGS+=(--subscription "$subscription")
done
if [[ -n "$PUBLIC_HOST" ]]; then
  INSTALL_ARGS+=(--public-host "$PUBLIC_HOST")
fi
if ((FORCE == 1)); then
  INSTALL_ARGS+=(--force)
fi

"$TEMP_DIR/$ASSET" "${INSTALL_ARGS[@]}"
