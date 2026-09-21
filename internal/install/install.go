package install

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/faddey/xray-monitor/internal/config"
)

const (
	serviceUser = "xray-monitor"
	installBin  = "/usr/local/bin/xray-monitor"
	configDir   = "/etc/xray-monitor"
	configPath  = "/etc/xray-monitor/config.json"
	dataDir     = "/var/lib/xray-monitor"
	servicePath = "/etc/systemd/system/xray-monitor.service"
)

type Options struct {
	Subscriptions []string
	PublicHost    string
	XrayBinary    string
	Force         bool
}

type Result struct {
	URL   string
	Token string
}

func Run(options Options) (Result, error) {
	if os.Geteuid() != 0 {
		return Result{}, errors.New("install must be run as root")
	}
	if len(options.Subscriptions) == 0 {
		return Result{}, errors.New("at least one -subscription is required")
	}
	if options.XrayBinary == "" {
		options.XrayBinary = "xray"
	}
	xrayPath, err := exec.LookPath(options.XrayBinary)
	if err != nil {
		return Result{}, fmt.Errorf("xray is not installed or not executable: %w", err)
	}
	xrayPath, err = filepath.Abs(xrayPath)
	if err != nil {
		return Result{}, err
	}
	if !options.Force {
		for _, path := range []string{configPath, servicePath} {
			if _, err := os.Stat(path); err == nil {
				return Result{}, fmt.Errorf("%s already exists; use -force to replace the installation", path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return Result{}, err
			}
		}
	}

	account, err := ensureUser()
	if err != nil {
		return Result{}, err
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return Result{}, err
	}
	if err := os.Chown(configDir, 0, gid); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return Result{}, err
	}
	if err := os.Chown(dataDir, uid, gid); err != nil {
		return Result{}, err
	}

	port, err := availablePort()
	if err != nil {
		return Result{}, err
	}
	token, err := randomToken()
	if err != nil {
		return Result{}, err
	}
	cfg := config.Defaults()
	cfg.Listen = net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	cfg.APIToken = token
	cfg.Subscriptions = options.Subscriptions
	cfg.DatabasePath = filepath.Join(dataDir, "monitor.db")
	cfg.XrayWorkDir = filepath.Join(dataDir, "xray-runtime")
	cfg.XrayBinary = xrayPath
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	configJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Result{}, err
	}
	configJSON = append(configJSON, '\n')
	if err := writeAtomic(configPath, configJSON, 0o640); err != nil {
		return Result{}, err
	}
	if err := os.Chown(configPath, 0, gid); err != nil {
		return Result{}, err
	}

	self, err := os.Executable()
	if err != nil {
		return Result{}, err
	}
	if err := copyAtomic(self, installBin, 0o755); err != nil {
		return Result{}, fmt.Errorf("install binary: %w", err)
	}
	service := `[Unit]
Description=Xray subscription availability monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=xray-monitor
Group=xray-monitor
ExecStart=/usr/local/bin/xray-monitor run -config /etc/xray-monitor/config.json
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
ReadWritePaths=/var/lib/xray-monitor

[Install]
WantedBy=multi-user.target
`
	if err := writeAtomic(servicePath, []byte(service), 0o644); err != nil {
		return Result{}, err
	}
	if output, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "enable", "xray-monitor.service").CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("enable service: %w: %s", err, strings.TrimSpace(string(output)))
	}
	// restart also starts an inactive unit and ensures -force upgrades do not
	// leave the previous executable/configuration running.
	if output, err := exec.Command("systemctl", "restart", "xray-monitor.service").CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("start service: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if options.PublicHost == "" {
		options.PublicHost, _ = os.Hostname()
	}
	return Result{URL: "http://" + net.JoinHostPort(options.PublicHost, strconv.Itoa(port)) + "/api/v1/status", Token: token}, nil
}

func ensureUser() (*user.User, error) {
	account, err := user.Lookup(serviceUser)
	if err == nil {
		return account, nil
	}
	if _, lookErr := exec.LookPath("useradd"); lookErr != nil {
		return nil, errors.New("system user xray-monitor does not exist and useradd was not found")
	}
	output, err := exec.Command("useradd", "--system", "--user-group", "--home-dir", dataDir, "--shell", "/usr/sbin/nologin", serviceUser).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("create system user: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return user.Lookup(serviceUser)
}

func availablePort() (int, error) {
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func copyAtomic(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".xray-monitor-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".xray-monitor-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
