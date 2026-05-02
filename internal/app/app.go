package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
	"easy_proxies/internal/subscription"
)

// Run builds the runtime components from config and blocks until shutdown.
func Run(ctx context.Context, cfg *config.Config) error {
	var dataStore store.Store
	if cfg.DatabasePath != "" {
		st, err := store.Open(cfg.DatabasePath)
		if err != nil {
			log.Printf("⚠️  SQLite store disabled: %v", err)
		} else {
			dataStore = st
		}
	}
	return RunWithStore(ctx, cfg, dataStore)
}

// RunWithStore builds the runtime components from an already-open SQLite store.
func RunWithStore(ctx context.Context, cfg *config.Config, dataStore store.Store) error {
	if dataStore != nil {
		defer dataStore.Close()
		if cfgFromStore, err := config.RuntimeFromStore(ctx, dataStore); err != nil {
			log.Printf("⚠️  Failed to load runtime settings from store: %v", err)
		} else {
			cfgFromStore.DatabasePath = cfg.DatabasePath
			cfg = cfgFromStore
		}
		if err := applyStoreNodeState(ctx, cfg, dataStore); err != nil {
			log.Printf("⚠️  Failed to apply store node state: %v", err)
		}
	}

	// Build monitor config
	proxyUsername := cfg.Listener.Username
	proxyPassword := cfg.Listener.Password
	if cfg.Mode == "multi-port" || cfg.Mode == "hybrid" {
		proxyUsername = cfg.MultiPort.Username
		proxyPassword = cfg.MultiPort.Password
	}

	monitorCfg := monitor.Config{
		Enabled:       cfg.ManagementEnabled(),
		Listen:        cfg.Management.Listen,
		ProbeTarget:   cfg.Management.ProbeTarget,
		Password:      cfg.Management.Password,
		ProxyUsername: proxyUsername,
		ProxyPassword: proxyPassword,
		ExternalIP:    cfg.ExternalIP,
	}

	// Create and start BoxManager
	var boxOpts []boxmgr.Option
	if dataStore != nil {
		boxOpts = append(boxOpts, boxmgr.WithStore(dataStore))
	}
	boxMgr := boxmgr.New(cfg, monitorCfg, boxOpts...)
	if err := boxMgr.Start(ctx); err != nil {
		return fmt.Errorf("start box manager: %w", err)
	}
	defer boxMgr.Close()

	statsCtx, stopStatsFlush := context.WithCancel(ctx)
	defer stopStatsFlush()
	if dataStore != nil {
		go periodicStatsFlush(statsCtx, boxMgr)
	}

	// Wire up config to monitor server for settings API
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetConfig(cfg)
	}

	// Always create SubscriptionManager so WebUI can hot-reload subscription config
	var subOpts []subscription.Option
	if dataStore != nil {
		subOpts = append(subOpts, subscription.WithStore(dataStore))
	}
	subMgr := subscription.New(cfg, boxMgr, subOpts...)
	defer subMgr.Stop()

	if cfg.SubscriptionRefresh.Enabled {
		configured, err := hasActiveSubscriptionSources(ctx, dataStore)
		if err != nil {
			log.Printf("⚠️  Failed to inspect subscription sources: %v", err)
		} else if configured {
			subMgr.Start()
		}
	}

	// Wire up subscription manager to monitor server for API endpoints
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetSubscriptionRefresher(subMgr)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
		fmt.Println("Context cancelled, initiating graceful shutdown...")
	case sig := <-sigCh:
		fmt.Printf("Received %s, initiating graceful shutdown...\n", sig)
	}

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Graceful shutdown sequence
	fmt.Println("Stopping subscription manager...")
	if subMgr != nil {
		subMgr.Stop()
	}

	stopStatsFlush()
	boxMgr.FlushStatsToStore(shutdownCtx)

	fmt.Println("Stopping box manager...")
	if err := boxMgr.Close(); err != nil {
		fmt.Printf("Error closing box manager: %v\n", err)
	}

	// Wait for connections to drain
	fmt.Println("Waiting for connections to drain...")
	select {
	case <-time.After(2 * time.Second):
		fmt.Println("Graceful shutdown completed")
	case <-shutdownCtx.Done():
		fmt.Println("Shutdown timeout exceeded, forcing exit")
	}

	return nil
}

func applyStoreNodeState(ctx context.Context, cfg *config.Config, s store.Store) error {
	if cfg == nil || s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	storeNodes, err := s.ListNodes(ctx, store.NodeFilter{})
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
		if storeNode, ok := storeByURI[node.URI]; ok && !storeNode.Enabled {
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
		filtered = append(filtered, config.NodeConfig{
			Name:     node.Name,
			URI:      node.URI,
			Port:     node.Port,
			Username: node.Username,
			Password: node.Password,
			Source:   config.NodeSource(node.Source),
		})
	}
	cfg.Nodes = filtered
	return nil
}

func hasActiveSubscriptionSources(ctx context.Context, s store.Store) (bool, error) {
	if s == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sources, err := s.ListSubscriptionSources(ctx)
	if err != nil {
		return false, err
	}
	for _, source := range sources {
		if source.Enabled && source.URL != "" {
			return true, nil
		}
	}
	return false, nil
}

func periodicStatsFlush(ctx context.Context, boxMgr *boxmgr.Manager) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			boxMgr.FlushStatsToStore(ctx)
		}
	}
}
