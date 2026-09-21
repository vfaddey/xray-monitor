package xray

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	binary  string
	workDir string
	logger  *slog.Logger
	mu      sync.Mutex
	current *process
}

type process struct {
	cmd     *exec.Cmd
	config  string
	done    chan struct{}
	errMu   sync.Mutex
	waitErr error
}

func NewManager(binary, workDir string, logger *slog.Logger) *Manager {
	return &Manager{binary: binary, workDir: workDir, logger: logger}
}

func (m *Manager) CheckBinary() error {
	path, err := exec.LookPath(m.binary)
	if err != nil {
		return fmt.Errorf("find xray binary %q: %w", m.binary, err)
	}
	m.binary = path
	return nil
}

func (m *Manager) Replace(ctx context.Context, config []byte) error {
	if err := os.MkdirAll(m.workDir, 0o750); err != nil {
		return err
	}
	hash := sha256.Sum256(config)
	path := filepath.Join(m.workDir, fmt.Sprintf("xray-%x.json", hash[:8]))
	if err := writeAtomic(path, config, 0o600); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}

	p := &process{config: path, done: make(chan struct{})}
	p.cmd = exec.Command(m.binary, "run", "-c", path)
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := p.cmd.Start(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("start xray: %w", err)
	}
	go func() {
		err := p.cmd.Wait()
		p.errMu.Lock()
		p.waitErr = err
		p.errMu.Unlock()
		close(p.done)
	}()

	startup := time.NewTimer(900 * time.Millisecond)
	defer startup.Stop()
	select {
	case <-ctx.Done():
		stopProcess(p)
		_ = os.Remove(path)
		return ctx.Err()
	case <-p.done:
		_ = os.Remove(path)
		return fmt.Errorf("xray exited during startup: %v", p.err())
	case <-startup.C:
	}

	m.mu.Lock()
	old := m.current
	m.current = p
	m.mu.Unlock()
	if old != nil {
		stopProcess(old)
		if old.config != path {
			_ = os.Remove(old.config)
		}
	}
	m.removeStaleConfigs(path)
	m.logger.Info("xray configuration activated", "config", path)
	return nil
}

func (m *Manager) removeStaleConfigs(current string) {
	paths, err := filepath.Glob(filepath.Join(m.workDir, "xray-*.json"))
	if err != nil {
		return
	}
	for _, path := range paths {
		if path != current {
			_ = os.Remove(path)
		}
	}
}

func (m *Manager) Alive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	select {
	case <-m.current.done:
		return false
	default:
		return true
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	p := m.current
	m.current = nil
	m.mu.Unlock()
	if p != nil {
		stopProcess(p)
		_ = os.Remove(p.config)
	}
}

func (p *process) err() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.waitErr
}

func stopProcess(p *process) {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".xray-config-*")
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
