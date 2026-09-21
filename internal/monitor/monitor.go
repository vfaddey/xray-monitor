package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/faddey/xray-monitor/internal/config"
	"github.com/faddey/xray-monitor/internal/model"
	"github.com/faddey/xray-monitor/internal/store"
	"github.com/faddey/xray-monitor/internal/subscription"
	xraycore "github.com/faddey/xray-monitor/internal/xray"
)

type Monitor struct {
	cfg       config.Config
	store     *store.Store
	xray      *xraycore.Manager
	fetcher   *subscription.Client
	logger    *slog.Logger
	mu        sync.RWMutex
	runtime   []xraycore.RuntimeNode
	states    map[string]model.NodeStatus
	sources   map[string][]subscription.Node
	lastSig   string
	subStatus model.SubscriptionStatus
	refreshCh chan struct{}
	checkCh   chan struct{}
}

func New(cfg config.Config, database *store.Store, manager *xraycore.Manager, logger *slog.Logger) *Monitor {
	return &Monitor{
		cfg:       cfg,
		store:     database,
		xray:      manager,
		fetcher:   subscription.NewClient(cfg.Timeout()),
		logger:    logger,
		states:    make(map[string]model.NodeStatus),
		sources:   make(map[string][]subscription.Node),
		refreshCh: make(chan struct{}, 1),
		checkCh:   make(chan struct{}, 1),
	}
}

func (m *Monitor) Run(ctx context.Context) {
	if err := m.Refresh(ctx); err != nil {
		m.logger.Error("initial subscription refresh failed", "error", err)
	}
	if len(m.runtimeSnapshot()) > 0 {
		m.Check(ctx)
	}
	refreshTicker := time.NewTicker(m.cfg.RefreshEvery())
	checkTicker := time.NewTicker(m.cfg.CheckEvery())
	defer refreshTicker.Stop()
	defer checkTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshTicker.C:
			if err := m.Refresh(ctx); err != nil {
				m.logger.Error("subscription refresh failed", "error", err)
			}
		case <-checkTicker.C:
			m.Check(ctx)
		case <-m.refreshCh:
			if err := m.Refresh(ctx); err != nil {
				m.logger.Error("requested subscription refresh failed", "error", err)
			}
		case <-m.checkCh:
			m.Check(ctx)
		}
	}
}

func (m *Monitor) Refresh(ctx context.Context) error {
	now := time.Now().UTC()
	m.mu.Lock()
	m.subStatus.LastAttempt = &now
	m.mu.Unlock()

	var fetchErrors []error
	anySuccess := false
	for i, source := range m.cfg.Subscriptions {
		nodes, err := m.fetcher.Fetch(ctx, source)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("subscription %d: %w", i+1, err))
			continue
		}
		m.sources[source] = nodes
		anySuccess = true
	}
	all := mergeNodes(m.sources)
	joinedErr := errors.Join(fetchErrors...)
	if len(all) == 0 {
		if joinedErr == nil {
			joinedErr = errors.New("subscriptions returned no supported nodes")
		}
		m.setSubscriptionError(joinedErr)
		_ = m.store.RecordSubscription(ctx, now, 0, joinedErr)
		return joinedErr
	}
	if !anySuccess && joinedErr != nil {
		m.setSubscriptionError(joinedErr)
		_ = m.store.RecordSubscription(ctx, now, len(all), joinedErr)
		return joinedErr
	}

	sig := signature(all)
	m.mu.RLock()
	unchanged := sig == m.lastSig
	m.mu.RUnlock()
	if !unchanged || !m.xray.Alive() {
		runtime, err := allocateRuntime(all, m.cfg.ProxyListen)
		if err != nil {
			m.setSubscriptionError(err)
			return err
		}
		xrayConfig, err := xraycore.BuildConfig(runtime, m.cfg.ProxyListen, m.cfg.XrayLogLevel)
		if err != nil {
			return err
		}
		if err := m.xray.Replace(ctx, xrayConfig); err != nil {
			m.setSubscriptionError(err)
			_ = m.store.RecordSubscription(ctx, now, len(all), err)
			return err
		}
		m.activate(runtime, sig)
		m.logger.Info("proxy set updated", "nodes", len(runtime))
	}
	if err := m.store.SyncNodes(ctx, all, now); err != nil {
		m.logger.Error("persist node set", "error", err)
	}
	if joinedErr == nil {
		m.mu.Lock()
		m.subStatus.LastSuccess = &now
		m.subStatus.Error = ""
		m.mu.Unlock()
	} else {
		m.setSubscriptionError(joinedErr)
	}
	_ = m.store.RecordSubscription(ctx, now, len(all), joinedErr)
	return joinedErr
}

func (m *Monitor) Check(ctx context.Context) {
	nodes := m.runtimeSnapshot()
	if len(nodes) == 0 {
		return
	}
	jobs := make(chan xraycore.RuntimeNode)
	results := make(chan model.Check, len(nodes))
	workers := min(m.cfg.Concurrency, len(nodes))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for node := range jobs {
				results <- m.checkOne(ctx, node)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, node := range nodes {
			select {
			case jobs <- node:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(results)
	checks := make([]model.Check, 0, len(nodes))
	for result := range results {
		checks = append(checks, result)
		m.applyCheck(result)
	}
	if err := m.store.RecordChecks(ctx, checks); err != nil && !errors.Is(err, context.Canceled) {
		m.logger.Error("persist checks", "error", err)
	}
}

func (m *Monitor) checkOne(ctx context.Context, node xraycore.RuntimeNode) model.Check {
	started := time.Now()
	result := model.Check{NodeID: node.Node.ID, CheckedAt: started.UTC()}
	proxyURL := &url.URL{Scheme: "socks5", Host: net.JoinHostPort(m.cfg.ProxyListen, fmt.Sprint(node.LocalPort))}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxyURL),
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   m.cfg.Timeout(),
		ResponseHeaderTimeout: m.cfg.Timeout(),
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: m.cfg.Timeout()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.CheckURL, nil)
	if err == nil {
		var resp *http.Response
		resp, err = client.Do(req)
		if resp != nil {
			result.StatusCode = resp.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
		}
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if result.StatusCode < 200 || result.StatusCode >= 400 {
		result.Error = fmt.Sprintf("unexpected HTTP status %d", result.StatusCode)
		return result
	}
	result.Available = true
	return result
}

func (m *Monitor) Status() model.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := model.Status{
		GeneratedAt:  time.Now().UTC(),
		XrayRunning:  m.xray.Alive(),
		Subscription: m.subStatus,
		Nodes:        make([]model.NodeStatus, 0, len(m.states)),
	}
	for _, node := range m.states {
		status.Nodes = append(status.Nodes, node)
		if node.Available == nil {
			status.Summary.Unknown++
		} else if *node.Available {
			status.Summary.Up++
		} else {
			status.Summary.Down++
		}
	}
	sort.Slice(status.Nodes, func(i, j int) bool {
		if status.Nodes[i].Name == status.Nodes[j].Name {
			return status.Nodes[i].ID < status.Nodes[j].ID
		}
		return status.Nodes[i].Name < status.Nodes[j].Name
	})
	status.Summary.Total = len(status.Nodes)
	return status
}

func (m *Monitor) TriggerRefresh() bool { return trigger(m.refreshCh) }
func (m *Monitor) TriggerCheck() bool   { return trigger(m.checkCh) }

func trigger(ch chan struct{}) bool {
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *Monitor) activate(runtime []xraycore.RuntimeNode, sig string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := make(map[string]model.NodeStatus, len(runtime))
	for _, item := range runtime {
		if old, ok := m.states[item.Node.ID]; ok {
			old.Name = item.Node.Name
			old.Address = item.Node.Address
			old.Port = item.Node.Port
			states[item.Node.ID] = old
			continue
		}
		states[item.Node.ID] = model.NodeStatus{ID: item.Node.ID, Name: item.Node.Name, Address: item.Node.Address, Port: item.Node.Port}
	}
	m.runtime = runtime
	m.states = states
	m.lastSig = sig
}

func (m *Monitor) applyCheck(check model.Check) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[check.NodeID]
	if !ok {
		return
	}
	available := check.Available
	latency := check.LatencyMS
	checkedAt := check.CheckedAt
	state.Available = &available
	state.LatencyMS = &latency
	state.StatusCode = check.StatusCode
	state.LastChecked = &checkedAt
	state.Error = check.Error
	m.states[check.NodeID] = state
}

func (m *Monitor) runtimeSnapshot() []xraycore.RuntimeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]xraycore.RuntimeNode(nil), m.runtime...)
}

func (m *Monitor) setSubscriptionError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subStatus.Error = err.Error()
}

func mergeNodes(sources map[string][]subscription.Node) []subscription.Node {
	byID := make(map[string]subscription.Node)
	for _, nodes := range sources {
		for _, node := range nodes {
			byID[node.ID] = node
		}
	}
	result := make([]subscription.Node, 0, len(byID))
	for _, node := range byID {
		result = append(result, node)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func signature(nodes []subscription.Node) string {
	var b strings.Builder
	for _, node := range nodes {
		b.WriteString(node.ID)
		b.WriteByte(0)
		b.WriteString(node.Name)
		b.WriteByte(0)
	}
	return b.String()
}

func allocateRuntime(nodes []subscription.Node, listenIP string) ([]xraycore.RuntimeNode, error) {
	listeners := make([]net.Listener, 0, len(nodes))
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	result := make([]xraycore.RuntimeNode, 0, len(nodes))
	for _, node := range nodes {
		listener, err := net.Listen("tcp", net.JoinHostPort(listenIP, "0"))
		if err != nil {
			return nil, fmt.Errorf("allocate local proxy port: %w", err)
		}
		listeners = append(listeners, listener)
		port := listener.Addr().(*net.TCPAddr).Port
		result = append(result, xraycore.RuntimeNode{Node: node, LocalPort: port})
	}
	return result, nil
}
