package boxmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/builder"
	"easy_proxies/internal/config"
	"easy_proxies/internal/geoip"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/outbound/pool"
	"easy_proxies/internal/store"

	"github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
)

// Ensure Manager implements monitor.NodeManager.
var _ monitor.NodeManager = (*Manager)(nil)

const (
	defaultDrainTimeout       = 10 * time.Second
	defaultHealthCheckTimeout = 30 * time.Second
	healthCheckPollInterval   = 500 * time.Millisecond
)

// Logger defines logging interface for the manager.
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

// WithStore enables optional SQLite-backed node state persistence.
func WithStore(s store.Store) Option {
	return func(m *Manager) { m.store = s }
}

// Manager owns the lifecycle of the active sing-box instance.
type Manager struct {
	mu sync.RWMutex

	currentBox    *box.Box
	monitorMgr    *monitor.Manager
	monitorServer *monitor.Server
	geoRouter     *geoip.Router
	cfg           *config.Config
	monitorCfg    monitor.Config
	store         store.Store

	drainTimeout      time.Duration
	minAvailableNodes int
	logger            Logger

	baseCtx context.Context
}

// New creates a BoxManager with the given config.
func New(cfg *config.Config, monitorCfg monitor.Config, opts ...Option) *Manager {
	m := &Manager{
		cfg:        cfg,
		monitorCfg: monitorCfg,
	}
	m.applyConfigSettings(cfg)
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = defaultLogger{}
	}
	if m.drainTimeout <= 0 {
		m.drainTimeout = defaultDrainTimeout
	}
	return m
}

// Start creates and starts the initial sing-box instance.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.ensureMonitor(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	if m.cfg == nil {
		m.mu.Unlock()
		return errors.New("box manager requires config")
	}
	if m.currentBox != nil {
		m.mu.Unlock()
		return errors.New("sing-box already running")
	}
	m.applyConfigSettings(m.cfg)
	m.baseCtx = ctx
	cfg := m.cfg
	m.mu.Unlock()

	if m.store != nil {
		if err := m.applyStoreNodeState(ctx, cfg); err != nil {
			m.logger.Warnf("failed to apply store node state: %v", err)
		}
	}
	if err := cfg.NormalizeWithPortMap(cfg.BuildPortMap()); err != nil {
		return fmt.Errorf("normalize config: %w", err)
	}

	if len(cfg.Nodes) == 0 {
		m.mu.Lock()
		m.cfg = cfg
		m.mu.Unlock()
		if m.monitorMgr != nil {
			m.monitorMgr.ClearNodes()
		}
		if m.monitorServer != nil {
			m.monitorServer.SetConfig(cfg)
		}
		m.logger.Infof("no proxy nodes configured; management server is running without sing-box listeners")
		return nil
	}

	// Try to start, with automatic port conflict resolution
	var instance *box.Box
	maxRetries := 10
	for retry := 0; retry < maxRetries; retry++ {
		var err error
		instance, err = m.createBox(ctx, cfg)
		if err != nil {
			return err
		}
		if err = instance.Start(); err != nil {
			_ = instance.Close()
			// Check if it's a port conflict error
			if conflictPort := extractPortFromBindError(err); conflictPort > 0 {
				m.logger.Warnf("port %d is in use, reassigning and retrying...", conflictPort)
				if reassigned := reassignConflictingPort(cfg, conflictPort); reassigned {
					pool.ResetSharedStateStore() // Reset shared state for rebuild
					continue
				}
			}
			return fmt.Errorf("start sing-box: %w", err)
		}
		break // Success
	}

	m.mu.Lock()
	m.currentBox = instance
	m.mu.Unlock()

	m.restoreMonitorTrafficFromStore(ctx)

	// Start periodic health check after nodes are registered
	m.mu.Lock()
	if m.monitorMgr != nil {
		m.monitorMgr.StartPeriodicHealthCheck(
			healthCheckInterval(cfg),
			healthCheckTimeout(cfg),
			healthCheckConcurrency(cfg),
		)
	}
	m.mu.Unlock()

	// Wait for initial health check if min nodes configured
	if cfg.SubscriptionRefresh.MinAvailableNodes > 0 {
		timeout := cfg.SubscriptionRefresh.HealthCheckTimeout
		if timeout <= 0 {
			timeout = defaultHealthCheckTimeout
		}
		if err := m.waitForHealthCheck(timeout); err != nil {
			m.logger.Warnf("initial health check warning: %v", err)
			// Don't fail startup, just warn
		}
	}

	m.logger.Infof("sing-box instance started with %d nodes", len(cfg.Nodes))

	// Start GeoIP router if enabled
	if cfg.GeoIP.Enabled {
		m.startGeoIPRouter(ctx, cfg)
	}

	return nil
}

// Reload gracefully switches to a new configuration.
// Node-local listeners require stopping the old instance first to release ports.
func (m *Manager) Reload(newCfg *config.Config) error {
	if newCfg == nil {
		return errors.New("new config is nil")
	}

	m.mu.Lock()
	ctx := m.baseCtx
	oldBox := m.currentBox
	oldCfg := m.cfg
	m.currentBox = nil // Mark as reloading
	m.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}

	m.logger.Infof("reloading with %d nodes", len(newCfg.Nodes))
	m.FlushStatsToStore(ctx)

	// Close the old instance first so node-local listener ports are released.
	// This causes a brief interruption but avoids port conflicts.
	if oldBox != nil {
		m.logger.Infof("stopping old instance to release ports...")
		if err := oldBox.Close(); err != nil {
			m.logger.Warnf("error closing old instance: %v", err)
		}
	}

	// Stop GeoIP router before starting new box to release its port
	m.mu.Lock()
	if m.geoRouter != nil {
		m.geoRouter.Stop()
		m.geoRouter = nil
	}
	m.mu.Unlock()

	// Give OS time to release ports
	time.Sleep(500 * time.Millisecond)

	// Reset shared state store to ensure clean state for new config
	pool.ResetSharedStateStore()

	// Clear stale monitor nodes so the dashboard reflects the new config
	if m.monitorMgr != nil {
		m.monitorMgr.ClearNodes()
	}

	if len(newCfg.Nodes) == 0 {
		m.applyConfigSettings(newCfg)
		m.mu.Lock()
		m.cfg = newCfg
		m.currentBox = nil
		m.mu.Unlock()
		if m.monitorServer != nil {
			m.monitorServer.SetConfig(m.cfg)
		}
		m.logger.Infof("reload completed with no proxy nodes; sing-box listeners are stopped")
		return nil
	}

	// Create and start new box instance with automatic port conflict resolution
	var instance *box.Box
	maxRetries := 10
	for retry := 0; retry < maxRetries; retry++ {
		var err error
		instance, err = m.createBox(ctx, newCfg)
		if err != nil {
			m.rollbackToOldConfig(ctx, oldCfg)
			return fmt.Errorf("create new box: %w", err)
		}
		if err = instance.Start(); err != nil {
			_ = instance.Close()
			// Check if it's a port conflict error
			if conflictPort := extractPortFromBindError(err); conflictPort > 0 {
				m.logger.Warnf("port %d is in use, reassigning and retrying...", conflictPort)
				if reassigned := reassignConflictingPort(newCfg, conflictPort); reassigned {
					pool.ResetSharedStateStore()
					continue
				}
			}
			m.rollbackToOldConfig(ctx, oldCfg)
			return fmt.Errorf("start new box: %w", err)
		}
		break // Success
	}

	m.applyConfigSettings(newCfg)

	m.mu.Lock()
	m.currentBox = instance
	m.cfg = newCfg
	m.mu.Unlock()

	// Sync config to monitor server so future WebUI settings changes target the current config pointer
	if m.monitorServer != nil {
		m.monitorServer.SetConfig(m.cfg)
	}

	m.restoreMonitorTrafficFromStore(ctx)

	// Restart periodic health checks for newly registered nodes and current settings.
	if m.monitorMgr != nil {
		m.monitorMgr.StartPeriodicHealthCheck(
			healthCheckInterval(newCfg),
			healthCheckTimeout(newCfg),
			healthCheckConcurrency(newCfg),
		)
	}

	m.logger.Infof("reload completed successfully with %d nodes", len(newCfg.Nodes))

	// Restart GeoIP router with new pools
	if newCfg.GeoIP.Enabled {
		m.startGeoIPRouter(ctx, newCfg)
	} else {
		m.mu.Lock()
		if m.geoRouter != nil {
			m.geoRouter.Stop()
			m.geoRouter = nil
		}
		m.mu.Unlock()
	}

	return nil
}

// rollbackToOldConfig attempts to restart with the previous configuration.
func (m *Manager) rollbackToOldConfig(ctx context.Context, oldCfg *config.Config) {
	if oldCfg == nil {
		return
	}
	m.logger.Warnf("attempting rollback to previous config...")
	instance, err := m.createBox(ctx, oldCfg)
	if err != nil {
		m.logger.Errorf("rollback failed to create box: %v", err)
		return
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		m.logger.Errorf("rollback failed to start box: %v", err)
		return
	}
	m.mu.Lock()
	m.currentBox = instance
	m.cfg = oldCfg
	m.mu.Unlock()
	// Sync config pointer to monitor server after rollback
	if m.monitorServer != nil {
		m.monitorServer.SetConfig(m.cfg)
	}
	m.logger.Infof("rollback successful")
}

// Close terminates the active instance and auxiliary components.
func (m *Manager) Close() error {
	m.FlushStatsToStore(context.Background())

	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	if m.currentBox != nil {
		err = m.currentBox.Close()
		m.currentBox = nil
	}
	if m.monitorServer != nil {
		m.monitorServer.Shutdown(context.Background())
		m.monitorServer = nil
	}
	if m.monitorMgr != nil {
		m.monitorMgr.Stop()
		m.monitorMgr = nil
	}
	if m.geoRouter != nil {
		m.geoRouter.Stop()
		m.geoRouter = nil
	}
	m.baseCtx = nil
	return err
}

// MonitorManager returns the shared monitor manager.
func (m *Manager) MonitorManager() *monitor.Manager {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.monitorMgr
}

// MonitorServer returns the monitor HTTP server.
func (m *Manager) MonitorServer() *monitor.Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.monitorServer
}

// FlushStatsToStore writes the current monitor runtime stats to the optional store.
func (m *Manager) FlushStatsToStore(ctx context.Context) {
	ctx = storeContext(ctx)

	m.mu.RLock()
	st := m.store
	monitorMgr := m.monitorMgr
	logger := m.logger
	m.mu.RUnlock()

	if st == nil || monitorMgr == nil {
		return
	}
	flushMonitorStatsToStore(ctx, monitorMgr, st, logger)
}

func (m *Manager) restoreMonitorTrafficFromStore(ctx context.Context) {
	ctx = storeContext(ctx)

	m.mu.RLock()
	st := m.store
	monitorMgr := m.monitorMgr
	logger := m.logger
	m.mu.RUnlock()

	if st == nil || monitorMgr == nil {
		return
	}

	storeNodes, err := st.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		warnf(logger, "failed to list store nodes for traffic restore: %v", err)
		return
	}
	statsByID, err := st.GetAllNodeStats(ctx)
	if err != nil {
		warnf(logger, "failed to list node stats for traffic restore: %v", err)
		return
	}

	lookup := buildStoreNodeLookup(storeNodes)
	for _, snap := range monitorMgr.Snapshot() {
		nodeID, ok := lookupNodeID(lookup, snap)
		if !ok {
			continue
		}
		stats := statsByID[nodeID]
		if stats == nil {
			continue
		}
		if err := monitorMgr.SetTraffic(snap.Tag, stats.TotalUploadBytes, stats.TotalDownloadBytes); err != nil {
			warnf(logger, "failed to restore traffic for %s: %v", snap.Tag, err)
		}
	}
}

type storeNodeLookup struct {
	byURI  map[string]int64
	byName map[string]int64
}

func buildStoreNodeLookup(nodes []store.Node) storeNodeLookup {
	lookup := storeNodeLookup{
		byURI:  make(map[string]int64, len(nodes)),
		byName: make(map[string]int64, len(nodes)),
	}
	for _, node := range nodes {
		if node.URI != "" {
			lookup.byURI[node.URI] = node.ID
		}
		if node.Name != "" {
			lookup.byName[node.Name] = node.ID
		}
	}
	return lookup
}

func lookupNodeID(lookup storeNodeLookup, snap monitor.Snapshot) (int64, bool) {
	if snap.URI != "" {
		if id, ok := lookup.byURI[snap.URI]; ok {
			return id, true
		}
	}
	if snap.Name != "" {
		if id, ok := lookup.byName[snap.Name]; ok {
			return id, true
		}
	}
	if snap.Tag != "" {
		if id, ok := lookup.byName[snap.Tag]; ok {
			return id, true
		}
	}
	return 0, false
}

func flushMonitorStatsToStore(ctx context.Context, monitorMgr *monitor.Manager, st store.Store, logger Logger) {
	snapshots := monitorMgr.Snapshot()
	if len(snapshots) == 0 {
		return
	}

	storeNodes, err := st.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		warnf(logger, "failed to list store nodes for stats flush: %v", err)
		return
	}
	lookup := buildStoreNodeLookup(storeNodes)

	updates := make([]store.StatsUpdate, 0, len(snapshots))
	for _, snap := range snapshots {
		nodeID, ok := lookupNodeID(lookup, snap)
		if !ok || nodeID == 0 {
			continue
		}
		updates = append(updates, store.StatsUpdate{
			NodeID:             nodeID,
			FailureCount:       snap.FailureCount,
			SuccessCount:       snap.SuccessCount,
			Blacklisted:        snap.Blacklisted,
			BlacklistedUntil:   snap.BlacklistedUntil,
			LastError:          snap.LastError,
			LastFailureAt:      snap.LastFailure,
			LastSuccessAt:      snap.LastSuccess,
			LastLatencyMs:      snap.LastLatencyMs,
			Available:          snap.Available,
			InitialCheckDone:   snap.InitialCheckDone,
			TotalUploadBytes:   snap.TotalUpload,
			TotalDownloadBytes: snap.TotalDownload,
		})
	}

	if len(updates) == 0 {
		return
	}
	if err := st.BatchUpdateStats(ctx, updates); err != nil {
		warnf(logger, "failed to flush node stats to store: %v", err)
	}
}

func warnf(logger Logger, format string, args ...any) {
	if logger != nil {
		logger.Warnf(format, args...)
	}
}

// startGeoIPRouter starts the GeoIP region-routing HTTP proxy server.
func (m *Manager) startGeoIPRouter(ctx context.Context, cfg *config.Config) {
	// Stop existing router if any
	m.mu.Lock()
	if m.geoRouter != nil {
		m.geoRouter.Stop()
		m.geoRouter = nil
	}
	m.mu.Unlock()

	geoipPort := cfg.GeoIP.Port
	if geoipPort == 0 {
		geoipPort = 1221 // Default GeoIP router port
	}
	for _, proxyPool := range cfg.ProxyPools {
		if proxyPool.Enabled && geoipPort == proxyPool.Listener.Port {
			geoipPort = 1221
			if geoipPort == proxyPool.Listener.Port {
				geoipPort = proxyPool.Listener.Port + 1
			}
			log.Printf("⚠️  GeoIP port conflicts with proxy pool port %d, using %d instead", proxyPool.Listener.Port, geoipPort)
			break
		}
	}
	geoipListen := cfg.GeoIP.Listen
	if geoipListen == "" {
		if proxyPool := firstEnabledProxyPool(cfg.ProxyPools); proxyPool != nil {
			geoipListen = proxyPool.Listener.Address
		} else {
			geoipListen = cfg.Listener.Address
		}
	}

	username, password := "", ""
	if proxyPool := firstEnabledProxyPool(cfg.ProxyPools); proxyPool != nil {
		username = proxyPool.Listener.Username
		password = proxyPool.Listener.Password
	}
	routerCfg := geoip.RouterConfig{
		Listen:   geoipListen,
		Port:     geoipPort,
		Username: username,
		Password: password,
	}

	router := geoip.NewRouter(routerCfg, nil)

	for _, proxyPool := range cfg.ProxyPools {
		if !proxyPool.Enabled {
			continue
		}
		poolPath := fmt.Sprintf("%d", proxyPool.ID)
		globalTag := fmt.Sprintf("proxy-pool-%d", proxyPool.ID)
		if dialer, ok := pool.GetDialer(globalTag); ok {
			router.SetPoolRoute(poolPath, "", dialer)
			if router.DefaultRoute() == "" {
				router.SetDefaultRoute(poolPath)
				router.SetGlobalPool(dialer)
			}
		}
		for _, region := range geoip.AllRegions() {
			poolTag := fmt.Sprintf("proxy-pool-%d-%s", proxyPool.ID, region)
			if dialer, ok := pool.GetDialer(poolTag); ok {
				router.SetPoolRoute(poolPath, region, dialer)
				log.Printf("   GeoIP: registered pool %s for path /%s/%s", poolTag, poolPath, region)
			}
		}
	}

	if err := router.Start(ctx); err != nil {
		m.logger.Warnf("failed to start GeoIP router: %v", err)
		return
	}

	m.mu.Lock()
	m.geoRouter = router
	m.mu.Unlock()
}

func firstEnabledProxyPool(pools []config.ProxyPoolConfig) *config.ProxyPoolConfig {
	for idx := range pools {
		if pools[idx].Enabled {
			return &pools[idx]
		}
	}
	return nil
}

// createBox builds a sing-box instance from config.
// It retries automatically when individual outbounds fail sing-box validation,
// removing the offending outbound each time.
func (m *Manager) createBox(ctx context.Context, cfg *config.Config) (*box.Box, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if m.monitorMgr == nil {
		return nil, errors.New("monitor manager not initialized")
	}

	opts, err := builder.Build(cfg)
	if err != nil {
		return nil, fmt.Errorf("build sing-box options: %w", err)
	}

	maxRetries := len(cfg.Nodes)*3 + 50 // Dynamically scale retries to configuration size
	outboundErrRe := regexp.MustCompile(`initialize outbound\[(\d+)\]`)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		inboundRegistry := include.InboundRegistry()
		outboundRegistry := include.OutboundRegistry()
		pool.Register(outboundRegistry)
		endpointRegistry := include.EndpointRegistry()
		dnsRegistry := include.DNSTransportRegistry()
		serviceRegistry := include.ServiceRegistry()

		boxCtx := box.Context(ctx, inboundRegistry, outboundRegistry, endpointRegistry, dnsRegistry, serviceRegistry)
		boxCtx = monitor.ContextWith(boxCtx, m.monitorMgr)

		instance, err := box.New(box.Options{Context: boxCtx, Options: opts})
		if err == nil {
			if attempt > 0 {
				log.Printf("✅ sing-box instance created after removing %d invalid outbound(s)", attempt)
			}
			return instance, nil
		}

		// Check if this is an outbound initialization error we can recover from
		matches := outboundErrRe.FindStringSubmatch(err.Error())
		if matches == nil {
			return nil, fmt.Errorf("create sing-box instance: %w", err)
		}

		idx, convErr := strconv.Atoi(matches[1])
		if convErr != nil || idx < 0 || idx >= len(opts.Outbounds) {
			return nil, fmt.Errorf("create sing-box instance: %w", err)
		}

		badTag := opts.Outbounds[idx].Tag
		log.Printf("⚠️  Outbound '%s' failed sing-box validation: %v (removing and retrying)", badTag, err)

		// Remove the offending outbound
		opts.Outbounds = append(opts.Outbounds[:idx], opts.Outbounds[idx+1:]...)

		// Clean up pool outbounds that contained this tag
		var newOutbounds []option.Outbound
		var removedPoolTags []string
		for _, ob := range opts.Outbounds {
			if ob.Type == pool.Type {
				if poolOpts, ok := ob.Options.(*pool.Options); ok {
					poolOpts.Members = removeFromSlice(poolOpts.Members, badTag)
					delete(poolOpts.Metadata, badTag)

					// If the pool is now empty, remove it to avoid another validation error
					if len(poolOpts.Members) == 0 {
						log.Printf("⚠️  Removing empty pool '%s'", ob.Tag)
						removedPoolTags = append(removedPoolTags, ob.Tag)
						continue // skip adding this empty pool
					}
				}
			}
			newOutbounds = append(newOutbounds, ob)
		}
		opts.Outbounds = newOutbounds

		// Also remove any routes that pointed to the removed pools or the badTag
		if (len(removedPoolTags) > 0 || badTag != "") && opts.Route != nil {
			removedSet := make(map[string]bool)
			for _, t := range removedPoolTags {
				removedSet[t] = true
			}
			removedSet[badTag] = true

			var newRules []option.Rule
			for _, r := range opts.Route.Rules {
				// We expect DefaultRules in our builder
				if r.Type == C.RuleTypeDefault {
					outboundTarget := r.DefaultOptions.RuleAction.RouteOptions.Outbound
					if !removedSet[outboundTarget] {
						newRules = append(newRules, r)
					} else {
						// Remove this rule since it points to a deleted outbound
					}
				} else {
					newRules = append(newRules, r)
				}
			}
			opts.Route.Rules = newRules
		}
	}

	return nil, fmt.Errorf("create sing-box instance: too many invalid outbounds (exceeded %d retries)", maxRetries)
}

// gracefulSwitch swaps the current box with a new one.
func (m *Manager) gracefulSwitch(newBox *box.Box) error {
	if newBox == nil {
		return errors.New("new box is nil")
	}

	m.mu.Lock()
	old := m.currentBox
	m.currentBox = newBox
	drainTimeout := m.drainTimeout
	m.mu.Unlock()

	if old != nil {
		go m.drainOldBox(old, drainTimeout)
	}

	m.logger.Infof("switched to new instance, draining old for %s", drainTimeout)
	return nil
}

// drainOldBox waits for drain timeout then closes the old box.
func (m *Manager) drainOldBox(oldBox *box.Box, timeout time.Duration) {
	if oldBox == nil {
		return
	}
	if timeout > 0 {
		time.Sleep(timeout)
	}
	if err := oldBox.Close(); err != nil {
		m.logger.Errorf("failed to close old instance: %v", err)
		return
	}
	m.logger.Infof("old instance closed after %s drain", timeout)
}

// removeFromSlice removes an element from a string slice.
func removeFromSlice(slice []string, element string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != element {
			result = append(result, s)
		}
	}
	return result
}

// waitForHealthCheck polls until enough nodes are available or timeout.
func (m *Manager) waitForHealthCheck(timeout time.Duration) error {
	if m.monitorMgr == nil || m.minAvailableNodes <= 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = defaultHealthCheckTimeout
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(healthCheckPollInterval)
	defer ticker.Stop()

	for {
		available, total := m.availableNodeCount()
		if available >= m.minAvailableNodes {
			m.logger.Infof("health check passed: %d/%d nodes available", available, total)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout: %d/%d nodes available (need >= %d)", available, total, m.minAvailableNodes)
		}
		<-ticker.C
	}
}

// availableNodeCount returns (available, total) node counts.
func (m *Manager) availableNodeCount() (int, int) {
	if m.monitorMgr == nil {
		return 0, 0
	}
	snapshots := m.monitorMgr.Snapshot()
	total := len(snapshots)
	available := 0
	for _, snap := range snapshots {
		if snap.InitialCheckDone && snap.Available {
			available++
		}
	}
	return available, total
}

// ensureMonitor initializes monitor manager and server if needed.
func (m *Manager) ensureMonitor(ctx context.Context) error {
	m.mu.Lock()
	if m.monitorMgr != nil {
		m.mu.Unlock()
		return nil
	}

	monitorMgr, err := monitor.NewManager(m.monitorCfg)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("init monitor manager: %w", err)
	}
	monitorMgr.SetLogger(monitorLoggerAdapter{logger: m.logger})
	m.monitorMgr = monitorMgr

	var serverToStart *monitor.Server
	if m.monitorCfg.Enabled {
		if m.monitorServer == nil {
			serverToStart = monitor.NewServer(m.monitorCfg, monitorMgr, log.Default())
			m.monitorServer = serverToStart
		}
		// Set config early so WebUI has data before Start() completes
		if m.monitorServer != nil && m.cfg != nil {
			m.monitorServer.SetConfig(m.cfg)
		}
		// Set NodeManager for config CRUD endpoints
		if m.monitorServer != nil {
			m.monitorServer.SetNodeManager(m)
			m.monitorServer.SetStore(m.store)
		}
		// Note: StartPeriodicHealthCheck is called after nodes are registered in Start()
	}
	m.mu.Unlock()

	if serverToStart != nil {
		serverToStart.Start(ctx)
	}
	return nil
}

// applyConfigSettings extracts runtime settings from config.
func (m *Manager) applyConfigSettings(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if cfg.SubscriptionRefresh.DrainTimeout > 0 {
		m.drainTimeout = cfg.SubscriptionRefresh.DrainTimeout
	} else if m.drainTimeout == 0 {
		m.drainTimeout = defaultDrainTimeout
	}
	m.minAvailableNodes = cfg.SubscriptionRefresh.MinAvailableNodes
}

func healthCheckInterval(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.HealthCheck.Interval > 0 {
		return cfg.HealthCheck.Interval
	}
	return 5 * time.Minute
}

func healthCheckTimeout(cfg *config.Config) time.Duration {
	if cfg != nil && cfg.HealthCheck.Timeout > 0 {
		return cfg.HealthCheck.Timeout
	}
	return 10 * time.Second
}

func healthCheckConcurrency(cfg *config.Config) int {
	if cfg != nil && cfg.HealthCheck.Concurrency > 0 {
		return cfg.HealthCheck.Concurrency
	}
	return 8
}

// defaultLogger is the fallback logger using standard log.
type defaultLogger struct{}

func (defaultLogger) Infof(format string, args ...any) {
	log.Printf("[boxmgr] "+format, args...)
}

func (defaultLogger) Warnf(format string, args ...any) {
	log.Printf("[boxmgr] WARN: "+format, args...)
}

func (defaultLogger) Errorf(format string, args ...any) {
	log.Printf("[boxmgr] ERROR: "+format, args...)
}

// monitorLoggerAdapter adapts Logger to monitor.Logger interface.
type monitorLoggerAdapter struct {
	logger Logger
}

func (a monitorLoggerAdapter) Info(args ...any) {
	if a.logger != nil {
		a.logger.Infof("%s", fmt.Sprint(args...))
	}
}

func (a monitorLoggerAdapter) Warn(args ...any) {
	if a.logger != nil {
		a.logger.Warnf("%s", fmt.Sprint(args...))
	}
}

// --- NodeManager interface implementation ---

var errConfigUnavailable = errors.New("config is not initialized")

// ListConfigNodes returns a copy of all configured nodes.
func (m *Manager) ListConfigNodes(ctx context.Context) ([]config.NodeConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.cfg == nil {
		return nil, errConfigUnavailable
	}
	if m.store == nil {
		return cloneNodes(m.cfg.Nodes), nil
	}
	ctx = storeContext(ctx)

	storeNodes, err := m.store.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		m.logger.Warnf("failed to list store nodes: %v", err)
		return cloneNodes(m.cfg.Nodes), nil
	}
	storeByURI := make(map[string]store.Node, len(storeNodes))
	for _, node := range storeNodes {
		storeByURI[node.URI] = node
	}

	result := make([]config.NodeConfig, 0, len(m.cfg.Nodes)+len(storeNodes))
	seen := make(map[string]struct{}, len(m.cfg.Nodes))
	for _, node := range m.cfg.Nodes {
		out := node
		if storeNode, ok := storeByURI[node.URI]; ok {
			out = m.storeNodeToConfig(storeNode)
		}
		result = append(result, out)
		seen[node.URI] = struct{}{}
	}
	for _, node := range storeNodes {
		if _, ok := seen[node.URI]; ok {
			continue
		}
		result = append(result, m.storeNodeToConfig(node))
	}
	for idx := range result {
		result[idx] = m.withDisplayOutboundJSON(result[idx])
	}
	return result, nil
}

// ParseNodeURI converts a supported proxy URI into structured sing-box outbound JSON.
func (m *Manager) ParseNodeURI(ctx context.Context, name string, uri string) (config.NodeConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return config.NodeConfig{}, err
		}
	}

	name = strings.TrimSpace(name)
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return config.NodeConfig{}, fmt.Errorf("%w: URI 不能为空", monitor.ErrInvalidNode)
	}
	if !config.IsProxyURI(uri) {
		return config.NodeConfig{}, fmt.Errorf("%w: 不支持的代理 URI", monitor.ErrInvalidNode)
	}
	if name == "" {
		name = config.ExtractNodeName(uri)
	}
	if name == "" {
		name = "node"
	}

	m.mu.RLock()
	skipCertVerify := false
	if m.cfg != nil {
		skipCertVerify = m.cfg.SkipCertVerify
	}
	m.mu.RUnlock()

	outboundJSON, err := builder.OutboundJSONFromURI(name, uri, skipCertVerify)
	if err != nil {
		return config.NodeConfig{}, fmt.Errorf("%w: %v", monitor.ErrInvalidNode, err)
	}
	return config.NodeConfig{Name: name, URI: uri, OutboundJSON: outboundJSON}, nil
}

// CreateNode adds a new node to the config and saves it.
func (m *Manager) CreateNode(ctx context.Context, node config.NodeConfig) (config.NodeConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return config.NodeConfig{}, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return config.NodeConfig{}, errConfigUnavailable
	}

	normalized, err := m.prepareNodeLocked(node, "")
	if err != nil {
		return config.NodeConfig{}, err
	}

	normalized.Source = config.NodeSourceManual

	m.cfg.Nodes = append(m.cfg.Nodes, normalized)
	storeNode, err := m.upsertStoreNode(ctx, normalized)
	if err != nil {
		m.cfg.Nodes = m.cfg.Nodes[:len(m.cfg.Nodes)-1]
		return config.NodeConfig{}, fmt.Errorf("save store node: %w", err)
	}
	if storeNode != nil {
		normalized.ID = storeNode.ID
		m.cfg.Nodes[len(m.cfg.Nodes)-1].ID = storeNode.ID
	}
	return normalized, nil
}

// UpdateNode updates an existing node by name and saves the config.
func (m *Manager) UpdateNode(ctx context.Context, name string, node config.NodeConfig) (config.NodeConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return config.NodeConfig{}, err
		}
	}

	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return config.NodeConfig{}, errConfigUnavailable
	}

	idx := m.nodeIndexLocked(name)
	if idx == -1 {
		return config.NodeConfig{}, monitor.ErrNodeNotFound
	}
	if m.cfg.Nodes[idx].Source == config.NodeSourceSubscription {
		return config.NodeConfig{}, monitor.ErrNodeReadOnly
	}

	normalized, err := m.prepareNodeLocked(node, name)
	if err != nil {
		return config.NodeConfig{}, err
	}

	// Preserve the original source
	normalized.Source = m.cfg.Nodes[idx].Source

	prev := m.cfg.Nodes[idx]
	m.cfg.Nodes[idx] = normalized
	storeNode, err := m.upsertStoreNode(ctx, normalized)
	if err != nil {
		m.cfg.Nodes[idx] = prev
		return config.NodeConfig{}, fmt.Errorf("save store node: %w", err)
	}
	if storeNode != nil {
		normalized.ID = storeNode.ID
		m.cfg.Nodes[idx].ID = storeNode.ID
	}
	return normalized, nil
}

// SetNodeEnabled updates a node enabled flag in runtime state and optional store.
func (m *Manager) SetNodeEnabled(ctx context.Context, name string, enabled bool) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return errConfigUnavailable
	}

	idx := m.nodeIndexLocked(name)
	storeNode, err := m.getStoreNodeByName(ctx, name)
	if err != nil {
		return err
	}
	if idx == -1 && storeNode == nil {
		return monitor.ErrNodeNotFound
	}

	if storeNode != nil {
		storeNode.Enabled = enabled
		if err := m.store.UpdateNode(storeContext(ctx), storeNode); err != nil {
			return fmt.Errorf("update store node: %w", err)
		}
	}

	if idx != -1 {
		m.cfg.Nodes[idx].Disabled = !enabled
		return nil
	}
	if enabled && storeNode != nil {
		m.cfg.Nodes = append(m.cfg.Nodes, m.storeNodeToConfig(*storeNode))
	}
	return nil
}

// DeleteNode removes a node by name and saves the config.
func (m *Manager) DeleteNode(ctx context.Context, name string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return errConfigUnavailable
	}

	idx := m.nodeIndexLocked(name)
	if idx == -1 {
		if storeNode, err := m.getStoreNodeByName(ctx, name); err != nil {
			return err
		} else if storeNode != nil {
			if storeNode.Source == store.NodeSourceSubscription {
				return monitor.ErrNodeReadOnly
			}
			return m.store.DeleteNode(storeContext(ctx), storeNode.ID)
		}
		return monitor.ErrNodeNotFound
	}
	if m.cfg.Nodes[idx].Source == config.NodeSourceSubscription {
		return monitor.ErrNodeReadOnly
	}

	backup := cloneNodes(m.cfg.Nodes)
	m.cfg.Nodes = append(m.cfg.Nodes[:idx], m.cfg.Nodes[idx+1:]...)
	if err := m.deleteStoreNode(ctx, name); err != nil {
		m.cfg.Nodes = backup
		return fmt.Errorf("delete store node: %w", err)
	}
	return nil
}

// TriggerReload reloads the sing-box instance with current config.
func (m *Manager) TriggerReload(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	m.mu.RLock()
	cfgCopy := m.copyConfigLocked()
	var portMap map[string]uint16
	if m.cfg != nil {
		portMap = m.cfg.BuildPortMap() // Preserve existing port assignments
	}
	m.mu.RUnlock()

	if cfgCopy == nil {
		return errConfigUnavailable
	}
	return m.ReloadWithPortMap(cfgCopy, portMap)
}

// ReloadWithPortMap gracefully switches to a new configuration, preserving port assignments.
func (m *Manager) ReloadWithPortMap(newCfg *config.Config, portMap map[string]uint16) error {
	if newCfg == nil {
		return errors.New("new config is nil")
	}

	if m.store != nil {
		if err := m.applyStoreNodeState(context.Background(), newCfg); err != nil {
			m.logger.Warnf("failed to apply store node state: %v", err)
		}
	}
	if err := newCfg.NormalizeWithPortMap(portMap); err != nil {
		return fmt.Errorf("normalize config with port map: %w", err)
	}

	return m.Reload(newCfg)
}

// CurrentPortMap returns the current port mapping from the active configuration.
func (m *Manager) CurrentPortMap() map[string]uint16 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg == nil {
		return nil
	}
	return m.cfg.BuildPortMap()
}

// CurrentConfig returns a copy of the current runtime config.
func (m *Manager) CurrentConfig() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.copyConfigLocked()
}

func (m *Manager) applyStoreNodeState(ctx context.Context, cfg *config.Config) error {
	if m.store == nil || cfg == nil {
		return nil
	}
	ctx = storeContext(ctx)

	storeNodes, err := m.store.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		return fmt.Errorf("list store nodes: %w", err)
	}
	storeByURI := make(map[string]store.Node, len(storeNodes))
	for _, node := range storeNodes {
		storeByURI[node.URI] = node
	}

	seen := make(map[string]struct{}, len(cfg.Nodes))
	filtered := cfg.Nodes[:0]
	for _, node := range cfg.Nodes {
		seen[node.URI] = struct{}{}
		if storeNode, ok := storeByURI[node.URI]; ok {
			if !storeNode.Enabled {
				continue
			}
			filtered = append(filtered, m.storeNodeToConfig(storeNode))
			continue
		}
		filtered = append(filtered, node)
	}
	for _, node := range storeNodes {
		if !node.Enabled {
			continue
		}
		if _, ok := seen[node.URI]; ok {
			continue
		}
		filtered = append(filtered, m.storeNodeToConfig(node))
	}
	cfg.Nodes = filtered
	return nil
}

func (m *Manager) upsertStoreNode(ctx context.Context, node config.NodeConfig) (*store.Node, error) {
	if m.store == nil {
		return nil, nil
	}
	ctx = storeContext(ctx)

	storeNode, err := m.store.GetNodeByURI(ctx, node.URI)
	if err != nil {
		return nil, fmt.Errorf("lookup store node: %w", err)
	}
	if storeNode == nil && node.Name != "" {
		storeNode, err = m.store.GetNodeByName(ctx, node.Name)
		if err != nil {
			return nil, fmt.Errorf("lookup store node by name: %w", err)
		}
	}
	if storeNode == nil {
		storeNode = &store.Node{
			URI:             node.URI,
			Name:            node.Name,
			Source:          string(node.Source),
			Port:            node.Port,
			Username:        node.Username,
			Password:        node.Password,
			InboundProtocol: node.InboundProtocol,
			OutboundJSON:    node.OutboundJSON,
			Enabled:         !node.Disabled,
		}
		if err := m.store.CreateNode(ctx, storeNode); err != nil {
			return nil, err
		}
		return storeNode, nil
	}

	storeNode.Name = node.Name
	storeNode.Source = string(node.Source)
	storeNode.Port = node.Port
	storeNode.Username = node.Username
	storeNode.Password = node.Password
	storeNode.InboundProtocol = node.InboundProtocol
	storeNode.OutboundJSON = node.OutboundJSON
	storeNode.Enabled = !node.Disabled
	if err := m.store.UpdateNode(ctx, storeNode); err != nil {
		return nil, err
	}
	return storeNode, nil
}

func (m *Manager) deleteStoreNode(ctx context.Context, name string) error {
	storeNode, err := m.getStoreNodeByName(ctx, name)
	if err != nil {
		return err
	}
	if storeNode == nil {
		return nil
	}
	return m.store.DeleteNode(storeContext(ctx), storeNode.ID)
}

func (m *Manager) getStoreNodeByName(ctx context.Context, name string) (*store.Node, error) {
	if m.store == nil {
		return nil, nil
	}
	storeNode, err := m.store.GetNodeByName(storeContext(ctx), name)
	if err != nil {
		return nil, fmt.Errorf("lookup store node: %w", err)
	}
	return storeNode, nil
}

func storeContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// --- Helper functions ---

// portBindErrorRegex matches "listen tcp4 0.0.0.0:24282: bind: address already in use"
var portBindErrorRegex = regexp.MustCompile(`listen tcp[46]? [^:]+:(\d+): bind: address already in use`)

// extractPortFromBindError extracts the port number from a bind error message.
func extractPortFromBindError(err error) uint16 {
	if err == nil {
		return 0
	}
	matches := portBindErrorRegex.FindStringSubmatch(err.Error())
	if len(matches) < 2 {
		return 0
	}
	var port int
	fmt.Sscanf(matches[1], "%d", &port)
	if port > 0 && port <= 65535 {
		return uint16(port)
	}
	return 0
}

// reassignConflictingPort finds the node using the conflicting port and assigns a new port.
func reassignConflictingPort(cfg *config.Config, conflictPort uint16) bool {
	// Build set of used ports
	usedPorts := make(map[uint16]bool)
	if port, ok := config.ListenPort(cfg.Management.Listen); ok {
		usedPorts[port] = true
	}
	for _, proxyPool := range cfg.ProxyPools {
		if proxyPool.Enabled && proxyPool.Listener.Port > 0 {
			usedPorts[proxyPool.Listener.Port] = true
		}
	}
	if cfg.GeoIP.Enabled && cfg.GeoIP.Port > 0 {
		usedPorts[cfg.GeoIP.Port] = true
	}
	for _, node := range cfg.Nodes {
		usedPorts[node.Port] = true
	}

	// Find and reassign the conflicting node
	for idx := range cfg.Nodes {
		if cfg.Nodes[idx].Port == conflictPort {
			// Find next available port
			newPort := conflictPort + 1
			address := cfg.MultiPort.Address
			if address == "" {
				address = "0.0.0.0"
			}
			for usedPorts[newPort] || !config.IsPortAvailable(address, newPort) {
				newPort++
				if newPort > 65535 {
					log.Printf("❌ No available port found for node %q", cfg.Nodes[idx].Name)
					return false
				}
			}
			log.Printf("⚠️  Port %d in use, reassigning node %q to port %d", conflictPort, cfg.Nodes[idx].Name, newPort)
			cfg.Nodes[idx].Port = newPort
			return true
		}
	}
	return false
}

func cloneNodes(nodes []config.NodeConfig) []config.NodeConfig {
	if len(nodes) == 0 {
		return []config.NodeConfig{} // Return empty slice, not nil, for proper JSON serialization
	}
	out := make([]config.NodeConfig, len(nodes))
	copy(out, nodes)
	return out
}

func cloneProxyPools(pools []config.ProxyPoolConfig) []config.ProxyPoolConfig {
	if len(pools) == 0 {
		return nil
	}
	out := make([]config.ProxyPoolConfig, len(pools))
	copy(out, pools)
	for idx := range out {
		out[idx].NodeIDs = append([]int64(nil), pools[idx].NodeIDs...)
	}
	return out
}

func (m *Manager) copyConfigLocked() *config.Config {
	if m.cfg == nil {
		return nil
	}
	cloned := *m.cfg
	cloned.Nodes = cloneNodes(m.cfg.Nodes)
	cloned.ProxyPools = cloneProxyPools(m.cfg.ProxyPools)
	return &cloned
}

func (m *Manager) nodeIndexLocked(name string) int {
	for idx, node := range m.cfg.Nodes {
		if node.Name == name {
			return idx
		}
	}
	return -1
}

func (m *Manager) portInUseLocked(port uint16, currentName string) bool {
	if port == 0 {
		return false
	}
	if managementPort, ok := config.ListenPort(m.cfg.Management.Listen); ok && managementPort == port {
		return true
	}
	if m.cfg.GeoIP.Enabled && m.cfg.GeoIP.Port == port {
		return true
	}
	for _, proxyPool := range m.cfg.ProxyPools {
		if proxyPool.Listener.Port == port {
			return true
		}
	}
	for _, node := range m.cfg.Nodes {
		if node.Name == currentName {
			continue
		}
		if node.Port == port {
			return true
		}
	}
	return false
}

func (m *Manager) nextAvailablePortLocked() uint16 {
	base := m.cfg.MultiPort.BasePort
	if base == 0 {
		base = 24000
	}
	used := make(map[uint16]struct{}, len(m.cfg.Nodes))
	if port, ok := config.ListenPort(m.cfg.Management.Listen); ok {
		used[port] = struct{}{}
	}
	if m.cfg.GeoIP.Enabled && m.cfg.GeoIP.Port > 0 {
		used[m.cfg.GeoIP.Port] = struct{}{}
	}
	for _, proxyPool := range m.cfg.ProxyPools {
		if proxyPool.Listener.Port > 0 {
			used[proxyPool.Listener.Port] = struct{}{}
		}
	}
	for _, node := range m.cfg.Nodes {
		if node.Port > 0 {
			used[node.Port] = struct{}{}
		}
	}
	port := base
	for i := 0; i < 1<<16; i++ {
		if _, ok := used[port]; !ok && port != 0 {
			return port
		}
		port++
		if port == 0 {
			port = 1
		}
	}
	return base
}

func (m *Manager) prepareNodeLocked(node config.NodeConfig, currentName string) (config.NodeConfig, error) {
	node.Name = strings.TrimSpace(node.Name)
	node.URI = strings.TrimSpace(node.URI)
	node.OutboundJSON = strings.TrimSpace(node.OutboundJSON)
	node.InboundProtocol = strings.TrimSpace(node.InboundProtocol)

	if node.URI == "" && node.OutboundJSON == "" {
		return config.NodeConfig{}, fmt.Errorf("%w: URI 或 JSON 不能为空", monitor.ErrInvalidNode)
	}

	// Extract name from URI if not provided
	if node.Name == "" {
		if currentName != "" {
			node.Name = currentName
		} else {
			node.Name = config.ExtractNodeName(node.URI)
		}
		// Fallback to auto-generated name
		if node.Name == "" {
			node.Name = fmt.Sprintf("node-%d", len(m.cfg.Nodes)+1)
		}
	}

	if node.OutboundJSON != "" {
		normalizedJSON, err := builder.NormalizeOutboundJSON(node.Name, node.OutboundJSON)
		if err != nil {
			return config.NodeConfig{}, fmt.Errorf("%w: %v", monitor.ErrInvalidNode, err)
		}
		node.OutboundJSON = normalizedJSON
	} else if node.URI != "" && !isGeneratedOutboundURI(node.URI) {
		outboundJSON, err := builder.OutboundJSONFromURI(node.Name, node.URI, m.cfg.SkipCertVerify)
		if err != nil {
			return config.NodeConfig{}, fmt.Errorf("%w: %v", monitor.ErrInvalidNode, err)
		}
		node.OutboundJSON = outboundJSON
	}

	if node.URI == "" {
		node.URI = m.generatedOutboundURI(currentName, node.OutboundJSON)
	}

	// Check for name conflict (excluding current node when updating)
	if idx := m.nodeIndexLocked(node.Name); idx != -1 {
		if currentName == "" || m.cfg.Nodes[idx].Name != currentName {
			return config.NodeConfig{}, fmt.Errorf("%w: 节点 %s 已存在", monitor.ErrNodeConflict, node.Name)
		}
	}
	if existing := m.nodeByKeyLocked(node.NodeKey(), currentName); existing != "" {
		return config.NodeConfig{}, fmt.Errorf("%w: 节点地址已被 %s 使用", monitor.ErrNodeConflict, existing)
	}

	if node.InboundProtocol != "" {
		protocol, err := config.NormalizeInboundProtocol(node.InboundProtocol)
		if err != nil {
			return config.NodeConfig{}, fmt.Errorf("%w: %v", monitor.ErrInvalidNode, err)
		}
		node.InboundProtocol = protocol
	}

	if node.Port > 0 {
		if m.portInUseLocked(node.Port, currentName) {
			return config.NodeConfig{}, fmt.Errorf("%w: 端口 %d 已被占用", monitor.ErrNodeConflict, node.Port)
		}
		if node.Username == "" {
			node.Username = m.cfg.MultiPort.Username
			node.Password = m.cfg.MultiPort.Password
		}
	}

	return node, nil
}

func (m *Manager) nodeByKeyLocked(nodeKey, currentName string) string {
	if nodeKey == "" {
		return ""
	}
	for _, existing := range m.cfg.Nodes {
		if existing.Name == currentName {
			continue
		}
		if existing.NodeKey() == nodeKey {
			return existing.Name
		}
	}
	return ""
}

func (m *Manager) storeNodeToConfig(node store.Node) config.NodeConfig {
	return config.NodeConfig{
		ID:              node.ID,
		Name:            node.Name,
		URI:             node.URI,
		OutboundJSON:    node.OutboundJSON,
		Port:            node.Port,
		InboundProtocol: node.InboundProtocol,
		Username:        node.Username,
		Password:        node.Password,
		Source:          config.NodeSource(node.Source),
		Disabled:        !node.Enabled,
	}
}

func (m *Manager) withDisplayOutboundJSON(node config.NodeConfig) config.NodeConfig {
	if node.OutboundJSON != "" || node.URI == "" || isGeneratedOutboundURI(node.URI) {
		return node
	}
	outboundJSON, err := builder.OutboundJSONFromURI(node.Name, node.URI, m.cfg.SkipCertVerify)
	if err != nil {
		return node
	}
	node.OutboundJSON = outboundJSON
	return node
}

func (m *Manager) generatedOutboundURI(currentName, outboundJSON string) string {
	if currentName != "" {
		if idx := m.nodeIndexLocked(currentName); idx != -1 && isGeneratedOutboundURI(m.cfg.Nodes[idx].URI) {
			return m.cfg.Nodes[idx].URI
		}
	}
	sum := sha256.Sum256([]byte(outboundJSON))
	return "json://outbound/" + hex.EncodeToString(sum[:16])
}

func isGeneratedOutboundURI(uri string) bool {
	return strings.HasPrefix(uri, "json://outbound/")
}
