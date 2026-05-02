package app

import (
	"context"
	"path/filepath"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

func TestApplyStoreNodeStateFiltersDisabledNodes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	cfg := &config.Config{
		Nodes: []config.NodeConfig{
			{Name: "enabled", URI: "http://user:pass@enabled.example.com:8080", Source: config.NodeSourceManual},
			{Name: "disabled", URI: "http://user:pass@disabled.example.com:8080", Source: config.NodeSourceManual},
		},
	}
	if err := st.CreateNode(ctx, &store.Node{
		URI:     cfg.Nodes[1].URI,
		Name:    cfg.Nodes[1].Name,
		Source:  store.NodeSourceManual,
		Enabled: false,
	}); err != nil {
		t.Fatalf("create disabled store node: %v", err)
	}

	if err := applyStoreNodeState(ctx, cfg, st); err != nil {
		t.Fatalf("apply store node state: %v", err)
	}

	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Name != "enabled" {
		t.Fatalf("filtered nodes = %+v, want only enabled node", cfg.Nodes)
	}
}
