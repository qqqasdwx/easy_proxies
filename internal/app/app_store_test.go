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

func TestApplyStoreNodeStateHydratesExistingNodeFields(t *testing.T) {
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

	storeNode := &store.Node{
		URI:             "http://user:pass@store.example.com:8080",
		Name:            "store-node",
		Source:          store.NodeSourceSubscription,
		Port:            25001,
		InboundProtocol: config.InboundProtocolHTTP,
		OutboundJSON:    `{"type":"http","tag":"store-node","server":"store.example.com","server_port":8080}`,
		Enabled:         true,
	}
	if err := st.CreateNode(ctx, storeNode); err != nil {
		t.Fatalf("create store node: %v", err)
	}

	cfg := &config.Config{Nodes: []config.NodeConfig{{
		Name:   "stale-node",
		URI:    storeNode.URI,
		Source: config.NodeSourceManual,
	}}}
	if err := applyStoreNodeState(ctx, cfg, st); err != nil {
		t.Fatalf("apply store node state: %v", err)
	}

	if len(cfg.Nodes) != 1 {
		t.Fatalf("filtered nodes = %+v, want one node", cfg.Nodes)
	}
	node := cfg.Nodes[0]
	if node.ID != storeNode.ID {
		t.Fatalf("node ID = %d, want %d", node.ID, storeNode.ID)
	}
	if node.Name != storeNode.Name || node.Source != config.NodeSourceSubscription {
		t.Fatalf("node identity = (%q, %q), want store identity", node.Name, node.Source)
	}
	if node.Port != storeNode.Port || node.InboundProtocol != storeNode.InboundProtocol || node.OutboundJSON != storeNode.OutboundJSON {
		t.Fatalf("node fields = %+v, want store fields", node)
	}
}
