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
			defer dataStore.Close()
			if err := syncStoreFromConfig(ctx, cfg, dataStore); err != nil {
				log.Printf("⚠️  Failed to sync nodes to store: %v", err)
			}
			if err := applyStoreNodeState(ctx, cfg, dataStore); err != nil {
				log.Printf("⚠️  Failed to apply store node state: %v", err)
			}
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
	subMgr := subscription.New(cfg, boxMgr)
	defer subMgr.Stop()

	// Start refresh loop only if subscriptions are already configured
	if cfg.SubscriptionRefresh.Enabled && len(cfg.Subscriptions) > 0 {
		subMgr.Start()
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

func syncStoreFromConfig(ctx context.Context, cfg *config.Config, s store.Store) error {
	if cfg == nil || s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	existing, err := s.ListNodes(ctx, store.NodeFilter{})
	if err != nil {
		return fmt.Errorf("list store nodes: %w", err)
	}
	byURI := make(map[string]store.Node, len(existing))
	for _, node := range existing {
		byURI[node.URI] = node
	}

	var upserts []store.Node
	for _, node := range cfg.Nodes {
		source := string(node.Source)
		if source == "" {
			source = store.NodeSourceInline
		}
		enabled := true
		if existingNode, ok := byURI[node.URI]; ok {
			enabled = existingNode.Enabled
		}
		upserts = append(upserts, store.Node{
			URI:      node.URI,
			Name:     node.Name,
			Source:   source,
			Port:     node.Port,
			Username: node.Username,
			Password: node.Password,
			Region:   "",
			Country:  "",
			Enabled:  enabled,
		})
	}
	return s.BulkUpsertNodes(ctx, upserts)
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
	enabledByURI := make(map[string]bool, len(storeNodes))
	for _, node := range storeNodes {
		enabledByURI[node.URI] = node.Enabled
	}

	filtered := cfg.Nodes[:0]
	for _, node := range cfg.Nodes {
		if enabled, ok := enabledByURI[node.URI]; ok && !enabled {
			continue
		}
		filtered = append(filtered, node)
	}
	cfg.Nodes = filtered
	return nil
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
