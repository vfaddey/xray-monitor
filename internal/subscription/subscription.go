package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxSubscriptionSize = 8 << 20

type Node struct {
	ID          string
	Name        string
	Address     string
	Port        int
	UUID        string
	Flow        string
	Network     string
	Security    string
	ServerName  string
	Fingerprint string
	PublicKey   string
	ShortID     string
	SpiderX     string
}

type Client struct {
	http *http.Client
}

func NewClient(timeout time.Duration) *Client {
	return &Client{http: &http.Client{Timeout: timeout}}
}

func (c *Client) Fetch(ctx context.Context, subscriptionURL string) ([]Node, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, subscriptionURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "xray-monitor/1")
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("fetch subscription: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch subscription: unexpected HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionSize+1))
	if err != nil {
		return nil, fmt.Errorf("read subscription: %w", err)
	}
	if len(body) > maxSubscriptionSize {
		return nil, errors.New("subscription response exceeds 8 MiB")
	}
	return Decode(body)
}

func Decode(body []byte) ([]Node, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, errors.New("empty subscription")
	}
	if !strings.Contains(text, "://") {
		decoded, err := decodeBase64(text)
		if err != nil {
			return nil, fmt.Errorf("decode subscription base64: %w", err)
		}
		text = string(decoded)
	}

	seen := make(map[string]struct{})
	var nodes []Node
	var parseErrors []string
	for lineNo, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		node, err := ParseVLESS(line)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: %v", lineNo+1, err))
			continue
		}
		if _, ok := seen[node.ID]; ok {
			continue
		}
		seen[node.ID] = struct{}{}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		if len(parseErrors) > 0 {
			return nil, fmt.Errorf("no supported VLESS nodes: %s", strings.Join(parseErrors, "; "))
		}
		return nil, errors.New("subscription contains no nodes")
	}
	return nodes, nil
}

func ParseVLESS(raw string) (Node, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Node{}, err
	}
	if u.Scheme != "vless" {
		return Node{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.User == nil || u.User.Username() == "" {
		return Node{}, errors.New("missing VLESS user id")
	}
	if !validUUID(u.User.Username()) {
		return Node{}, errors.New("invalid VLESS UUID")
	}
	if u.Hostname() == "" {
		return Node{}, errors.New("missing server address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return Node{}, errors.New("invalid server port")
	}
	q := u.Query()
	security := valueOr(q.Get("security"), "none")
	network := valueOr(q.Get("type"), "raw")
	if network == "tcp" {
		network = "raw"
	}
	if network != "raw" {
		return Node{}, fmt.Errorf("unsupported transport %q (only raw/tcp is currently supported)", network)
	}
	if security != "reality" {
		return Node{}, fmt.Errorf("unsupported security %q (only reality is currently supported)", security)
	}
	if q.Get("pbk") == "" || q.Get("sni") == "" {
		return Node{}, errors.New("reality requires pbk and sni")
	}

	canonical := *u
	canonical.Fragment = ""
	sum := sha256.Sum256([]byte(canonical.String()))
	name := strings.TrimSpace(u.Fragment)
	if name == "" {
		name = netLabel(u.Hostname(), port)
	}
	return Node{
		ID:          hex.EncodeToString(sum[:12]),
		Name:        name,
		Address:     u.Hostname(),
		Port:        port,
		UUID:        u.User.Username(),
		Flow:        q.Get("flow"),
		Network:     network,
		Security:    security,
		ServerName:  q.Get("sni"),
		Fingerprint: valueOr(q.Get("fp"), "chrome"),
		PublicKey:   q.Get("pbk"),
		ShortID:     q.Get("sid"),
		SpiderX:     q.Get("spx"),
	}, nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func decodeBase64(value string) ([]byte, error) {
	value = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, value)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func netLabel(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}
