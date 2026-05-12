package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
)

// Logger defines logging interface.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Option configures the Manager.
type Option func(*Manager)

// WithLogger sets a custom logger.
func WithLogger(l Logger) Option {
	return func(m *Manager) { m.logger = l }
}

// WithStore enables SQLite-backed subscription status and source loading.
func WithStore(s store.Store) Option {
	return func(m *Manager) { m.store = s }
}

// Manager handles periodic subscription refresh.
type Manager struct {
	mu sync.RWMutex

	baseCfg    *config.Config
	boxMgr     *boxmgr.Manager
	logger     Logger
	store      store.Store
	httpClient *http.Client // Custom HTTP client with connection pooling

	status        monitor.SubscriptionStatus
	ctx           context.Context
	cancel        context.CancelFunc
	refreshMu     sync.Mutex // prevents concurrent refreshes
	manualRefresh chan struct{}
	urls          []string
	nodesHash     string
}

type fetchedSourceNodes struct {
	source store.SubscriptionSource
	nodes  []config.NodeConfig
}

// New creates a SubscriptionManager.
func New(cfg *config.Config, boxMgr *boxmgr.Manager, opts ...Option) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	// Create optimized HTTP client with connection pooling
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second, // Overall timeout
	}

	m := &Manager{
		baseCfg:       cfg,
		boxMgr:        boxMgr,
		ctx:           ctx,
		cancel:        cancel,
		manualRefresh: make(chan struct{}, 1),
		httpClient:    httpClient,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = defaultLogger{}
	}
	m.loadPersistedStatus()
	return m
}

// Start begins the periodic refresh loop.
func (m *Manager) Start() {
	m.ReloadSchedule()
}

// ReloadSchedule restarts automatic refresh scheduling from subscription sources.
func (m *Manager) ReloadSchedule() {
	if m == nil {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.ctx = ctx
	m.cancel = cancel
	m.manualRefresh = make(chan struct{}, 1)
	m.mu.Unlock()

	sources, err := m.autoUpdateSources()
	if err != nil {
		m.logger.Warnf("failed to load auto-update subscription sources: %v", err)
		return
	}
	if len(sources) == 0 {
		m.logger.Infof("no auto-update subscriptions configured")
		m.syncNextRefreshStatus()
		return
	}

	m.logger.Infof("starting subscription auto refresh for %d sources", len(sources))
	m.syncNextRefreshStatus()
	go m.refreshLoop(ctx)
}

// RefreshSource refreshes one subscription source and reloads the proxy config.
func (m *Manager) RefreshSource(id int64) error {
	if m.store == nil {
		return fmt.Errorf("database store is required")
	}
	source, err := m.store.GetSubscriptionSource(context.Background(), id)
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("subscription source %d not found", id)
	}
	if strings.TrimSpace(source.URL) == "" {
		return fmt.Errorf("subscription source %d has empty url", id)
	}
	if !m.refreshMu.TryLock() {
		return fmt.Errorf("subscription refresh already in progress")
	}
	defer m.refreshMu.Unlock()

	timeout := m.currentConfig().SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	nodes, err := m.fetchSubscription(source.URL, timeout)
	if err != nil {
		m.updateSourceStatus(context.Background(), *source, 0, err)
		return err
	}
	for idx := range nodes {
		nodes[idx].Source = config.NodeSourceSubscription
	}
	if err := m.persistSubscriptionNodes(context.Background(), source.ID, nodes); err != nil {
		return err
	}
	m.updateSourceStatus(context.Background(), *source, len(nodes), nil)

	portMap := m.boxMgr.CurrentPortMap()
	newCfg := m.createNewConfig(nodes)
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		return err
	}

	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = len(nodes)
	m.status.LastError = ""
	m.status.RefreshCount++
	status := m.status
	hash := m.nodesHash
	m.mu.Unlock()
	m.persistStatus(context.Background(), status, hash)
	return nil
}

// Stop stops the periodic refresh.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	// Close idle connections
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

// RefreshNow triggers an immediate refresh.
func (m *Manager) RefreshNow() error {
	startCount := m.Status().RefreshCount
	m.doRefresh()
	status := m.Status()
	if status.RefreshCount == startCount {
		return fmt.Errorf("subscription refresh already in progress")
	}
	if status.LastError != "" {
		return fmt.Errorf("refresh failed: %s", status.LastError)
	}
	return nil
}

// Status returns the current refresh status.
func (m *Manager) Status() monitor.SubscriptionStatus {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()
	return status
}

func (m *Manager) refreshDueSubscriptions() {
	sources, err := m.autoUpdateSources()
	if err != nil {
		m.logger.Warnf("failed to load auto-update subscription sources: %v", err)
		return
	}
	if len(sources) == 0 {
		m.syncNextRefreshStatus()
		return
	}

	now := time.Now()
	for _, source := range sources {
		if !source.NextRefresh.IsZero() && source.NextRefresh.After(now) {
			continue
		}
		if err := m.RefreshSource(source.ID); err != nil {
			m.logger.Warnf("auto refresh subscription %d failed: %v", source.ID, err)
		}
	}
	m.syncNextRefreshStatus()
}

func (m *Manager) syncNextRefreshStatus() {
	sources, err := m.autoUpdateSources()
	if err != nil {
		return
	}
	var next time.Time
	for _, source := range sources {
		if source.NextRefresh.IsZero() {
			next = time.Now()
			break
		}
		if next.IsZero() || source.NextRefresh.Before(next) {
			next = source.NextRefresh
		}
	}
	m.mu.Lock()
	m.status.NextRefresh = next
	status := m.status
	hash := m.nodesHash
	m.mu.Unlock()
	m.persistStatus(context.Background(), status, hash)
}

// refreshLoop runs per-source automatic refresh checks.
func (m *Manager) refreshLoop(ctx context.Context) {
	m.mu.RLock()
	manualRefresh := m.manualRefresh
	m.mu.RUnlock()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	m.refreshDueSubscriptions()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshDueSubscriptions()
		case <-manualRefresh:
			m.doRefresh()
		}
	}
}

// doRefresh performs a single refresh operation.
func (m *Manager) doRefresh() {
	// Prevent concurrent refreshes
	if !m.refreshMu.TryLock() {
		m.logger.Warnf("refresh already in progress, skipping")
		return
	}
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	m.status.IsRefreshing = true
	status := m.status
	hash := m.nodesHash
	m.mu.Unlock()
	m.persistStatus(context.Background(), status, hash)

	defer func() {
		m.mu.Lock()
		m.status.IsRefreshing = false
		m.status.RefreshCount++
		status := m.status
		hash := m.nodesHash
		m.mu.Unlock()
		m.persistStatus(context.Background(), status, hash)
	}()

	m.logger.Infof("starting subscription refresh")

	groups, nodes, err := m.fetchAllSubscriptions()
	if err != nil {
		m.logger.Errorf("fetch subscriptions failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	if len(nodes) == 0 {
		m.logger.Warnf("no nodes fetched from subscriptions")
		m.mu.Lock()
		m.status.LastError = "no nodes fetched"
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.logger.Infof("fetched %d nodes from subscriptions", len(nodes))

	for idx := range nodes {
		nodes[idx].Source = config.NodeSourceSubscription
	}

	newHash := m.computeNodesHash(nodes)
	for _, group := range groups {
		for idx := range group.nodes {
			group.nodes[idx].Source = config.NodeSourceSubscription
		}
		if err := m.persistSubscriptionNodes(context.Background(), group.source.ID, group.nodes); err != nil {
			m.logger.Errorf("persist subscription nodes failed: %v", err)
			m.mu.Lock()
			m.status.LastError = err.Error()
			m.status.LastRefresh = time.Now()
			m.mu.Unlock()
			return
		}
		m.updateSourceStatus(context.Background(), group.source, len(group.nodes), nil)
	}
	m.mu.Lock()
	m.nodesHash = newHash
	m.mu.Unlock()

	// Get current port mapping to preserve existing node ports
	portMap := m.boxMgr.CurrentPortMap()

	// Create new config with updated nodes
	newCfg := m.createNewConfig(nodes)

	// Trigger BoxManager reload with port preservation
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		m.logger.Errorf("reload failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = len(nodes)
	m.status.LastError = ""
	m.mu.Unlock()

	m.logger.Infof("subscription refresh completed, %d nodes active", len(nodes))
}

// computeNodesHash computes a hash of node URIs for change detection.
func (m *Manager) computeNodesHash(nodes []config.NodeConfig) string {
	var uris []string
	for _, node := range nodes {
		uris = append(uris, node.URI)
	}
	content := strings.Join(uris, "\n")
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

func (m *Manager) loadPersistedStatus() {
	if m.store == nil {
		return
	}
	status, err := m.store.GetSubscriptionStatus(context.Background())
	if err != nil {
		m.logger.Warnf("failed to load subscription status: %v", err)
		return
	}
	m.mu.Lock()
	m.status.LastRefresh = status.LastRefresh
	m.status.NextRefresh = status.NextRefresh
	m.status.NodeCount = status.NodeCount
	m.status.LastError = status.LastError
	m.status.RefreshCount = status.RefreshCount
	m.status.IsRefreshing = status.IsRefreshing
	m.nodesHash = status.NodesHash
	m.mu.Unlock()
}

func (m *Manager) persistStatus(ctx context.Context, status monitor.SubscriptionStatus, hash string) {
	if m.store == nil {
		return
	}
	if err := m.store.UpdateSubscriptionStatus(ctx, &store.SubscriptionStatus{
		LastRefresh:  status.LastRefresh,
		NextRefresh:  status.NextRefresh,
		NodeCount:    status.NodeCount,
		LastError:    status.LastError,
		RefreshCount: status.RefreshCount,
		IsRefreshing: status.IsRefreshing,
		NodesHash:    hash,
	}); err != nil {
		m.logger.Warnf("failed to persist subscription status: %v", err)
	}
}

func (m *Manager) updateSourceStatus(ctx context.Context, source store.SubscriptionSource, nodeCount int, refreshErr error) {
	if m.store == nil || source.ID <= 0 {
		return
	}
	now := time.Now()
	source.LastRefresh = now
	source.NodeCount = nodeCount
	if source.AutoUpdate && source.Interval > 0 {
		source.NextRefresh = now.Add(source.Interval)
	} else {
		source.NextRefresh = time.Time{}
	}
	if refreshErr != nil {
		source.LastError = refreshErr.Error()
	} else {
		source.LastError = ""
	}
	if err := m.store.UpdateSubscriptionSource(ctx, &source); err != nil {
		m.logger.Warnf("failed to update subscription source status: %v", err)
	}
}

func (m *Manager) persistSubscriptionNodes(ctx context.Context, sourceID int64, nodes []config.NodeConfig) error {
	if m.store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return m.store.WithTx(ctx, func(tx store.Store) error {
		existing, err := tx.ListNodes(ctx, store.NodeFilter{
			Source:         store.NodeSourceSubscription,
			SubscriptionID: sourceID,
		})
		if err != nil {
			return fmt.Errorf("list subscription nodes: %w", err)
		}
		existingByURI := make(map[string]store.Node, len(existing))
		for _, node := range existing {
			existingByURI[node.URI] = node
		}

		nextURIs := make(map[string]struct{}, len(nodes))
		upserts := make([]store.Node, 0, len(nodes))
		for _, node := range nodes {
			enabled := true
			if old, ok := existingByURI[node.URI]; ok {
				enabled = old.Enabled
			}
			nextURIs[node.URI] = struct{}{}
			upserts = append(upserts, store.Node{
				URI:             node.URI,
				Name:            node.Name,
				Source:          store.NodeSourceSubscription,
				Port:            node.Port,
				InboundProtocol: node.InboundProtocol,
				Username:        node.Username,
				Password:        node.Password,
				OutboundJSON:    node.OutboundJSON,
				SubscriptionID:  sourceID,
				Enabled:         enabled,
			})
		}
		for _, node := range existing {
			if _, ok := nextURIs[node.URI]; ok {
				continue
			}
			if err := tx.DeleteNode(ctx, node.ID); err != nil {
				return fmt.Errorf("delete stale subscription node %q: %w", node.Name, err)
			}
		}
		if err := tx.BulkUpsertNodes(ctx, upserts); err != nil {
			return fmt.Errorf("upsert subscription nodes: %w", err)
		}
		return nil
	})
}

// fetchAllSubscriptions fetches nodes from all configured subscription URLs.
func (m *Manager) fetchAllSubscriptions() ([]fetchedSourceNodes, []config.NodeConfig, error) {
	var groups []fetchedSourceNodes
	var allNodes []config.NodeConfig
	var lastErr error

	timeout := m.currentConfig().SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	sources, err := m.subscriptionSources()
	if err != nil {
		return nil, nil, err
	}
	for _, source := range sources {
		nodes, err := m.fetchSubscription(source.URL, timeout)
		if err != nil {
			m.logger.Warnf("failed to fetch %s: %v", source.URL, err)
			m.updateSourceStatus(context.Background(), source, 0, err)
			lastErr = err
			continue
		}
		m.logger.Infof("fetched %d nodes from subscription", len(nodes))
		groups = append(groups, fetchedSourceNodes{source: source, nodes: nodes})
		allNodes = append(allNodes, nodes...)
	}

	if len(allNodes) == 0 && lastErr != nil {
		return nil, nil, lastErr
	}

	return groups, allNodes, nil
}

func (m *Manager) subscriptionSources() ([]store.SubscriptionSource, error) {
	if m.store == nil {
		m.mu.RLock()
		urls := append([]string(nil), m.urls...)
		m.mu.RUnlock()
		sources := make([]store.SubscriptionSource, 0, len(urls))
		for idx, u := range urls {
			if strings.TrimSpace(u) == "" {
				continue
			}
			sources = append(sources, store.SubscriptionSource{
				ID:         int64(idx + 1),
				Name:       fmt.Sprintf("subscription-%d", idx+1),
				URL:        u,
				Enabled:    true,
				AutoUpdate: true,
				Interval:   m.currentConfig().SubscriptionRefresh.Interval,
			})
		}
		return sources, nil
	}
	sources, err := m.store.ListSubscriptionSources(context.Background())
	if err != nil {
		return nil, err
	}
	filtered := sources[:0]
	for _, source := range sources {
		if source.Enabled && source.URL != "" {
			filtered = append(filtered, source)
		}
	}
	return filtered, nil
}

func (m *Manager) autoUpdateSources() ([]store.SubscriptionSource, error) {
	sources, err := m.subscriptionSources()
	if err != nil {
		return nil, err
	}
	filtered := sources[:0]
	for _, source := range sources {
		if !source.AutoUpdate || strings.TrimSpace(source.URL) == "" {
			continue
		}
		if source.Interval <= 0 {
			source.Interval = m.currentConfig().SubscriptionRefresh.Interval
			if source.Interval <= 0 {
				source.Interval = time.Hour
			}
		}
		filtered = append(filtered, source)
	}
	return filtered, nil
}

// fetchSubscription fetches and parses a single subscription URL.
func (m *Manager) fetchSubscription(subURL string, timeout time.Duration) ([]config.NodeConfig, error) {
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "clash-verge/v2.2.3")
	req.Header.Set("Accept", "*/*")

	// Use custom HTTP client with connection pooling
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Limit read size to prevent memory exhaustion
	const maxBodySize = 10 * 1024 * 1024 // 10MB
	limitedReader := io.LimitReader(resp.Body, maxBodySize)

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return config.ParseSubscriptionContent(string(body))
}

// createNewConfig creates a new config with updated nodes while preserving other settings.
func (m *Manager) createNewConfig(nodes []config.NodeConfig) *config.Config {
	// Start from the current runtime config so subscription refresh preserves
	// settings changed from the WebUI after process startup.
	newCfg := *m.currentConfig()

	// Process node names
	for i := range nodes {
		nodes[i].Name = strings.TrimSpace(nodes[i].Name)
		nodes[i].URI = strings.TrimSpace(nodes[i].URI)

		// Auto-extract name from URI if not provided
		if nodes[i].Name == "" {
			nodes[i].Name = config.ExtractNodeName(nodes[i].URI)
		}
		if nodes[i].Name == "" {
			nodes[i].Name = fmt.Sprintf("node-%d", i)
		}
	}

	newCfg.Nodes = nodes
	return &newCfg
}

func (m *Manager) currentConfig() *config.Config {
	if m != nil && m.boxMgr != nil {
		if cfg := m.boxMgr.CurrentConfig(); cfg != nil {
			return cfg
		}
	}
	if m != nil && m.baseCfg != nil {
		cloned := *m.baseCfg
		if len(m.baseCfg.Nodes) > 0 {
			cloned.Nodes = append([]config.NodeConfig(nil), m.baseCfg.Nodes...)
		}
		return &cloned
	}
	return &config.Config{}
}

type defaultLogger struct{}

func (defaultLogger) Infof(format string, args ...any) {
	log.Printf("[subscription] "+format, args...)
}

func (defaultLogger) Warnf(format string, args ...any) {
	log.Printf("[subscription] WARN: "+format, args...)
}

func (defaultLogger) Errorf(format string, args ...any) {
	log.Printf("[subscription] ERROR: "+format, args...)
}
