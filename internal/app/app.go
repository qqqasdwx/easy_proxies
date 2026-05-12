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
	proxyUsername, proxyPassword := monitorProxyCredentials(cfg)

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

	// Always create SubscriptionManager so WebUI subscription settings can schedule refreshes.
	var subOpts []subscription.Option
	if dataStore != nil {
		subOpts = append(subOpts, subscription.WithStore(dataStore))
	}
	subMgr := subscription.New(cfg, boxMgr, subOpts...)
	defer subMgr.Stop()
	subMgr.Start()

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

func monitorProxyCredentials(cfg *config.Config) (string, string) {
	if cfg == nil {
		return "", ""
	}
	for _, proxyPool := range cfg.ProxyPools {
		if proxyPool.Enabled && proxyPool.Listener.Username != "" {
			return proxyPool.Listener.Username, proxyPool.Listener.Password
		}
	}
	if cfg.MultiPort.Username != "" {
		return cfg.MultiPort.Username, cfg.MultiPort.Password
	}
	return cfg.Listener.Username, cfg.Listener.Password
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
		if storeNode, ok := storeByURI[node.URI]; ok {
			if !storeNode.Enabled {
				continue
			}
			filtered = append(filtered, storeNodeToConfig(storeNode))
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
		filtered = append(filtered, storeNodeToConfig(node))
	}
	cfg.Nodes = filtered
	return nil
}

func storeNodeToConfig(node store.Node) config.NodeConfig {
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
