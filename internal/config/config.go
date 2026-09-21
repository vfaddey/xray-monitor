package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen          string   `json:"listen"`
	APIToken        string   `json:"api_token"`
	Subscriptions   []string `json:"subscriptions"`
	RefreshInterval string   `json:"refresh_interval"`
	CheckInterval   string   `json:"check_interval"`
	CheckURL        string   `json:"check_url"`
	CheckTimeout    string   `json:"check_timeout"`
	Concurrency     int      `json:"concurrency"`
	DatabasePath    string   `json:"database_path"`
	XrayBinary      string   `json:"xray_binary"`
	XrayWorkDir     string   `json:"xray_work_dir"`
	ProxyListen     string   `json:"proxy_listen"`
	XrayLogLevel    string   `json:"xray_log_level"`
	refreshDuration time.Duration
	checkDuration   time.Duration
	timeoutDuration time.Duration
}

func Defaults() Config {
	return Config{
		Listen:          "127.0.0.1:9187",
		RefreshInterval: "5m",
		CheckInterval:   "1m",
		CheckURL:        "https://www.gstatic.com/generate_204",
		CheckTimeout:    "15s",
		Concurrency:     16,
		DatabasePath:    "./monitor.db",
		XrayBinary:      "xray",
		XrayWorkDir:     "./xray-runtime",
		ProxyListen:     "127.0.0.1",
		XrayLogLevel:    "warning",
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		errs = append(errs, fmt.Errorf("listen: %w", err))
	}
	if len(c.APIToken) < 24 {
		errs = append(errs, errors.New("api_token must contain at least 24 characters"))
	}
	if len(c.Subscriptions) == 0 {
		errs = append(errs, errors.New("at least one subscription is required"))
	}
	for i, raw := range c.Subscriptions {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("subscriptions[%d] must be an http(s) URL", i))
		}
	}
	var err error
	if c.refreshDuration, err = positiveDuration(c.RefreshInterval); err != nil {
		errs = append(errs, fmt.Errorf("refresh_interval: %w", err))
	}
	if c.checkDuration, err = positiveDuration(c.CheckInterval); err != nil {
		errs = append(errs, fmt.Errorf("check_interval: %w", err))
	}
	if c.timeoutDuration, err = positiveDuration(c.CheckTimeout); err != nil {
		errs = append(errs, fmt.Errorf("check_timeout: %w", err))
	}
	checkURL, err := url.Parse(c.CheckURL)
	if err != nil || (checkURL.Scheme != "http" && checkURL.Scheme != "https") || checkURL.Host == "" {
		errs = append(errs, errors.New("check_url must be an http(s) URL"))
	}
	if c.Concurrency < 1 || c.Concurrency > 512 {
		errs = append(errs, errors.New("concurrency must be between 1 and 512"))
	}
	if c.DatabasePath == "" || c.XrayWorkDir == "" || c.XrayBinary == "" {
		errs = append(errs, errors.New("database_path, xray_work_dir and xray_binary are required"))
	}
	if ip := net.ParseIP(c.ProxyListen); ip == nil || !ip.IsLoopback() {
		errs = append(errs, errors.New("proxy_listen must be a loopback IP address"))
	}
	switch c.XrayLogLevel {
	case "debug", "info", "warning", "error", "none":
	default:
		errs = append(errs, errors.New("xray_log_level must be debug, info, warning, error or none"))
	}
	return errors.Join(errs...)
}

func positiveDuration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("must be positive")
	}
	return d, nil
}

func (c Config) RefreshEvery() time.Duration { return c.refreshDuration }
func (c Config) CheckEvery() time.Duration   { return c.checkDuration }
func (c Config) Timeout() time.Duration      { return c.timeoutDuration }

func (c Config) EnsureDirectories() error {
	for _, dir := range []string{filepath.Dir(c.DatabasePath), c.XrayWorkDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return nil
}
