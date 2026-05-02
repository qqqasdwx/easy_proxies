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
	if !m.baseCfg.SubscriptionRefresh.Enabled {
		m.logger.Infof("subscription refresh disabled")
		return
	}
	sources, err := m.subscriptionSources()
	if err != nil {
		m.logger.Warnf("failed to load subscription sources: %v", err)
		return
	}
	if len(sources) == 0 {
		m.logger.Infof("no subscriptions configured, refresh disabled")
		return
	}

	interval := m.baseCfg.SubscriptionRefresh.Interval
	m.logger.Infof("starting subscription refresh, interval: %s", interval)

	go m.refreshLoop(interval)
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

	timeout := m.baseCfg.SubscriptionRefresh.Timeout
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

// UpdateConfig hot-reloads subscription URLs and refresh settings without restart.
func (m *Manager) UpdateConfig(urls []string, enabled bool, interval time.Duration) {
	m.mu.Lock()
	m.urls = append(m.urls[:0], urls...)
	m.baseCfg.SubscriptionRefresh.Enabled = enabled
	if interval > 0 {
		m.baseCfg.SubscriptionRefresh.Interval = interval
	}
	m.mu.Unlock()

	if m.store != nil {
		sources := make([]store.SubscriptionSource, 0, len(urls))
		for idx, u := range urls {
			sources = append(sources, store.SubscriptionSource{
				Name:       fmt.Sprintf("subscription-%d", idx+1),
				URL:        u,
				Enabled:    true,
				AutoUpdate: enabled,
				Interval:   interval,
			})
		}
		if err := m.store.ReplaceSubscriptionSources(context.Background(), sources); err != nil {
			m.logger.Errorf("failed to save subscription sources: %v", err)
		}
		if err := config.SaveRuntime(context.Background(), m.store, m.baseCfg); err != nil {
			m.logger.Errorf("failed to save runtime config: %v", err)
		}
	}

	// Restart the refresh loop with new settings
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.ctx = ctx
	m.cancel = cancel
	m.manualRefresh = make(chan struct{}, 1)
	m.mu.Unlock()

	if len(urls) == 0 {
		m.logger.Infof("no subscription URLs configured, skipping refresh")
		return
	}

	// Always start the refresh loop to handle the immediate refresh signal
	m.logger.Infof("subscription config updated: %d URLs, enabled=%v, interval=%s", len(urls), enabled, m.baseCfg.SubscriptionRefresh.Interval)
	go m.refreshLoop(m.baseCfg.SubscriptionRefresh.Interval)

	// Always trigger an immediate fetch when URLs are provided,
	// regardless of the "enabled" flag (which only controls periodic auto-refresh)
	select {
	case m.manualRefresh <- struct{}{}:
		m.logger.Infof("triggered immediate refresh after config update")
	default:
		// A refresh is already pending
	}
}

// UpdateConfigAndRefresh updates subscription config and synchronously waits for
// the first refresh to complete before returning. This ensures the caller (WebUI API)
// can confirm the update took effect.
func (m *Manager) UpdateConfigAndRefresh(urls []string, enabled bool, interval time.Duration) error {
	m.UpdateConfig(urls, enabled, interval)

	if len(urls) == 0 {
		return nil
	}

	// Wait for the refresh triggered by UpdateConfig to complete
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := timeout + m.baseCfg.SubscriptionRefresh.HealthCheckTimeout

	ctx, cancel := context.WithTimeout(m.ctx, deadline)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startCount := m.Status().RefreshCount
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("刷新超时")
		case <-ticker.C:
			status := m.Status()
			if status.RefreshCount > startCount {
				if status.LastError != "" {
					return fmt.Errorf("刷新失败: %s", status.LastError)
				}
				return nil
			}
		}
	}
}

// RefreshNow triggers an immediate refresh.
func (m *Manager) RefreshNow() error {
	select {
	case m.manualRefresh <- struct{}{}:
	default:
		// Already a refresh pending
	}

	// Wait for refresh to complete or timeout
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout+m.baseCfg.SubscriptionRefresh.HealthCheckTimeout)
	defer cancel()

	// Poll status until refresh completes
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startCount := m.Status().RefreshCount
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("refresh timeout")
		case <-ticker.C:
			status := m.Status()
			if status.RefreshCount > startCount {
				if status.LastError != "" {
					return fmt.Errorf("refresh failed: %s", status.LastError)
				}
				return nil
			}
		}
	}
}

// Status returns the current refresh status.
func (m *Manager) Status() monitor.SubscriptionStatus {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()
	return status
}

// refreshLoop runs the periodic refresh.
func (m *Manager) refreshLoop(interval time.Duration) {
	m.mu.RLock()
	autoEnabled := m.baseCfg.SubscriptionRefresh.Enabled
	m.mu.RUnlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if autoEnabled {
		// Update next refresh time only when auto-refresh is enabled
		m.mu.Lock()
		m.status.NextRefresh = time.Now().Add(interval)
		m.mu.Unlock()
	}

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			// Only do periodic refresh when auto-refresh is enabled
			if !autoEnabled {
				continue
			}
			m.doRefresh()
			m.mu.Lock()
			m.status.NextRefresh = time.Now().Add(interval)
			m.mu.Unlock()
		case <-m.manualRefresh:
			// Always honor manual/immediate refresh regardless of enabled flag
			m.doRefresh()
			if autoEnabled {
				ticker.Reset(interval)
				m.mu.Lock()
				m.status.NextRefresh = time.Now().Add(interval)
				m.mu.Unlock()
			}
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

	timeout := m.baseCfg.SubscriptionRefresh.Timeout
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
				Interval:   m.baseCfg.SubscriptionRefresh.Interval,
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
	// Deep copy base config
	newCfg := *m.baseCfg

	// Assign port numbers to nodes in multi-port mode
	if newCfg.Mode == "multi-port" {
		portCursor := newCfg.MultiPort.BasePort
		for i := range nodes {
			nodes[i].Port = portCursor
			portCursor++
			// Apply default credentials
			if nodes[i].Username == "" {
				nodes[i].Username = newCfg.MultiPort.Username
				nodes[i].Password = newCfg.MultiPort.Password
			}
		}
	}

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
