package boxmgr

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/store"
)

func TestListConfigNodesIncludesStoreDisabledNodes(t *testing.T) {
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

	disabled := store.Node{
		URI:     "http://user:pass@disabled.example.com:8080",
		Name:    "disabled",
		Source:  store.NodeSourceManual,
		Enabled: false,
	}
	if err := st.CreateNode(ctx, &disabled); err != nil {
		t.Fatalf("create disabled node: %v", err)
	}

	mgr := New(&config.Config{
		Nodes: []config.NodeConfig{{
			Name:   "enabled",
			URI:    "http://user:pass@enabled.example.com:8080",
			Source: config.NodeSourceManual,
		}},
	}, monitor.Config{}, WithStore(st))

	nodes, err := mgr.ListConfigNodes(ctx)
	if err != nil {
		t.Fatalf("list config nodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("node count = %d, want 2 (%+v)", len(nodes), nodes)
	}

	var foundDisabled bool
	for _, node := range nodes {
		if node.Name == "disabled" {
			foundDisabled = true
			if !node.Disabled {
				t.Fatalf("disabled node missing disabled flag: %+v", node)
			}
		}
	}
	if !foundDisabled {
		t.Fatalf("disabled store node not returned: %+v", nodes)
	}
}

func TestSetNodeEnabledPersistsManualSource(t *testing.T) {
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

	node := config.NodeConfig{
		Name:   "manual-node",
		URI:    "http://user:pass@example.com:8080",
		Source: config.NodeSourceManual,
	}
	if err := st.CreateNode(ctx, &store.Node{
		URI:     node.URI,
		Name:    node.Name,
		Source:  store.NodeSourceManual,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create store node: %v", err)
	}

	mgr := New(&config.Config{Nodes: []config.NodeConfig{node}}, monitor.Config{}, WithStore(st))
	if err := mgr.SetNodeEnabled(ctx, node.Name, false); err != nil {
		t.Fatalf("disable node: %v", err)
	}

	nodes, err := mgr.ListConfigNodes(ctx)
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Source != config.NodeSourceManual || !nodes[0].Disabled {
		t.Fatalf("unexpected listed node: %+v", nodes)
	}

	storeNode, err := st.GetNodeByURI(ctx, node.URI)
	if err != nil {
		t.Fatalf("get store node: %v", err)
	}
	if storeNode == nil || storeNode.Enabled {
		t.Fatalf("store node was not disabled: %+v", storeNode)
	}
}

func TestListConfigNodesHydratesStoreNodeID(t *testing.T) {
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
		URI:     "http://user:pass@hydrated.example.com:8080",
		Name:    "hydrated",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, storeNode); err != nil {
		t.Fatalf("create store node: %v", err)
	}

	mgr := New(&config.Config{Nodes: []config.NodeConfig{{
		Name: "hydrated",
		URI:  storeNode.URI,
	}}}, monitor.Config{}, WithStore(st))
	nodes, err := mgr.ListConfigNodes(ctx)
	if err != nil {
		t.Fatalf("list config nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want one node", nodes)
	}
	if nodes[0].ID != storeNode.ID {
		t.Fatalf("node ID = %d, want store ID %d", nodes[0].ID, storeNode.ID)
	}
	if nodes[0].Source != config.NodeSourceManual {
		t.Fatalf("node source = %q, want manual", nodes[0].Source)
	}
}

func TestDeleteNodeRemovesStoreOnlyNode(t *testing.T) {
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
		URI:     "http://user:pass@store-only.example.com:8080",
		Name:    "store-only",
		Source:  store.NodeSourceManual,
		Enabled: false,
	}
	if err := st.CreateNode(ctx, storeNode); err != nil {
		t.Fatalf("create store-only node: %v", err)
	}

	mgr := New(&config.Config{}, monitor.Config{}, WithStore(st))
	if err := mgr.DeleteNode(ctx, storeNode.Name); err != nil {
		t.Fatalf("delete store-only node: %v", err)
	}
	got, err := st.GetNodeByURI(ctx, storeNode.URI)
	if err != nil {
		t.Fatalf("get deleted store node: %v", err)
	}
	if got != nil {
		t.Fatalf("store-only node still exists: %+v", got)
	}
}

func TestSubscriptionNodesAreReadOnlyForEditAndDelete(t *testing.T) {
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

	node := config.NodeConfig{
		Name:   "sub-node",
		URI:    "http://user:pass@sub.example.com:8080",
		Source: config.NodeSourceSubscription,
	}
	if err := st.CreateNode(ctx, &store.Node{
		URI:     node.URI,
		Name:    node.Name,
		Source:  store.NodeSourceSubscription,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create subscription node: %v", err)
	}

	mgr := New(&config.Config{Nodes: []config.NodeConfig{node}}, monitor.Config{}, WithStore(st))
	if _, err := mgr.UpdateNode(ctx, node.Name, config.NodeConfig{Name: "renamed", URI: node.URI}); !errors.Is(err, monitor.ErrNodeReadOnly) {
		t.Fatalf("update error = %v, want read-only", err)
	}
	if err := mgr.DeleteNode(ctx, node.Name); !errors.Is(err, monitor.ErrNodeReadOnly) {
		t.Fatalf("delete error = %v, want read-only", err)
	}
}

func TestParseNodeURIExtractsNameAndOutboundJSON(t *testing.T) {
	ctx := context.Background()
	mgr := New(&config.Config{}, monitor.Config{})
	node, err := mgr.ParseNodeURI(ctx, "", "socks5://user:pass@127.0.0.1:1080#parsed")
	if err != nil {
		t.Fatalf("parse node uri: %v", err)
	}
	if node.Name != "parsed" || node.URI == "" || !strings.Contains(node.OutboundJSON, `"server": "127.0.0.1"`) {
		t.Fatalf("parsed node = %+v", node)
	}
}

func TestCreateJSONOnlyNodePersistsStructuredFields(t *testing.T) {
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

	mgr := New(&config.Config{
		Mode: "multi-port",
		MultiPort: config.MultiPortConfig{
			BasePort: 24000,
		},
	}, monitor.Config{}, WithStore(st))
	created, err := mgr.CreateNode(ctx, config.NodeConfig{
		Name:            "json-only",
		OutboundJSON:    `{"type":"socks","tag":"ignored","server":"127.0.0.1","server_port":1080}`,
		InboundProtocol: "socks5",
		Username:        "local-user",
		Password:        "local-pass",
	})
	if err != nil {
		t.Fatalf("create json-only node: %v", err)
	}
	if created.URI == "" || created.OutboundJSON == "" || created.InboundProtocol != "socks5" {
		t.Fatalf("created node missing structured fields: %+v", created)
	}

	storeNode, err := st.GetNodeByName(ctx, "json-only")
	if err != nil {
		t.Fatalf("get store node: %v", err)
	}
	if storeNode == nil || storeNode.URI != created.URI || storeNode.OutboundJSON == "" || storeNode.InboundProtocol != "socks5" {
		t.Fatalf("stored node = %+v, want structured fields", storeNode)
	}

	listed, err := mgr.ListConfigNodes(ctx)
	if err != nil {
		t.Fatalf("list config nodes: %v", err)
	}
	if len(listed) != 1 || listed[0].OutboundJSON == "" || listed[0].InboundProtocol != "socks5" {
		t.Fatalf("listed nodes = %+v", listed)
	}
}

func TestCreateNodeRejectsProxyPoolPortConflict(t *testing.T) {
	ctx := context.Background()
	mgr := New(&config.Config{
		ProxyPools: []config.ProxyPoolConfig{{
			Name:    "default",
			Enabled: true,
			Listener: config.ListenerConfig{
				Address:  "127.0.0.1",
				Port:     2323,
				Protocol: config.InboundProtocolMixed,
			},
			Mode:     "sequential",
			AllNodes: true,
		}},
	}, monitor.Config{})

	_, err := mgr.CreateNode(ctx, config.NodeConfig{
		Name: "conflict",
		URI:  "http://user:pass@example.com:8080",
		Port: 2323,
	})
	if !errors.Is(err, monitor.ErrNodeConflict) {
		t.Fatalf("create error = %v, want node conflict", err)
	}
	if err == nil || !strings.Contains(err.Error(), "端口 2323 已被占用") {
		t.Fatalf("create error = %v, want occupied port detail", err)
	}
}

func TestFirstEnabledProxyPoolSkipsDisabledPools(t *testing.T) {
	pool := firstEnabledProxyPool([]config.ProxyPoolConfig{
		{
			ID:      1,
			Name:    "disabled",
			Enabled: false,
			Listener: config.ListenerConfig{
				Address:  "127.0.0.1",
				Port:     2323,
				Username: "disabled-user",
			},
		},
		{
			ID:      2,
			Name:    "enabled",
			Enabled: true,
			Listener: config.ListenerConfig{
				Address:  "127.0.0.2",
				Port:     2324,
				Username: "enabled-user",
			},
		},
	})
	if pool == nil || pool.ID != 2 || pool.Listener.Username != "enabled-user" {
		t.Fatalf("first enabled pool = %+v, want pool 2", pool)
	}
}

func TestApplyStoreNodeStateAddsEnabledStoreNodes(t *testing.T) {
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
		URI:     "http://user:pass@store.example.com:8080",
		Name:    "store-node",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, storeNode); err != nil {
		t.Fatalf("create store node: %v", err)
	}

	cfg := &config.Config{}
	mgr := New(cfg, monitor.Config{}, WithStore(st))
	if err := mgr.applyStoreNodeState(ctx, cfg); err != nil {
		t.Fatalf("apply store node state: %v", err)
	}
	if len(cfg.Nodes) != 1 || cfg.Nodes[0].Name != "store-node" {
		t.Fatalf("cfg nodes = %+v, want store node", cfg.Nodes)
	}
}

func TestApplyStoreNodeStateHydratesExistingNodeID(t *testing.T) {
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
		URI:            "http://user:pass@subscription.example.com:8080",
		Name:           "subscription-node",
		Source:         store.NodeSourceSubscription,
		SubscriptionID: 7,
		Enabled:        true,
	}
	if err := st.CreateNode(ctx, storeNode); err != nil {
		t.Fatalf("create store node: %v", err)
	}

	cfg := &config.Config{Nodes: []config.NodeConfig{{
		Name:   "subscription-node",
		URI:    storeNode.URI,
		Source: config.NodeSourceSubscription,
	}}}
	mgr := New(cfg, monitor.Config{}, WithStore(st))
	if err := mgr.applyStoreNodeState(ctx, cfg); err != nil {
		t.Fatalf("apply store node state: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("cfg nodes = %+v, want one node", cfg.Nodes)
	}
	if cfg.Nodes[0].ID != storeNode.ID {
		t.Fatalf("node ID = %d, want store ID %d", cfg.Nodes[0].ID, storeNode.ID)
	}
	if cfg.Nodes[0].Source != config.NodeSourceSubscription {
		t.Fatalf("node source = %q, want subscription", cfg.Nodes[0].Source)
	}
}

func TestRestoreAndFlushTrafficStats(t *testing.T) {
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

	node := &store.Node{
		URI:     "http://user:pass@traffic.example.com:8080",
		Name:    "traffic-node",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := st.UpsertNodeStats(ctx, &store.NodeStats{
		NodeID:             node.ID,
		TotalUploadBytes:   1000,
		TotalDownloadBytes: 2000,
		LastLatencyMs:      -1,
	}); err != nil {
		t.Fatalf("seed stats: %v", err)
	}

	monitorMgr, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatalf("new monitor manager: %v", err)
	}
	monitorMgr.Stop()
	entry := monitorMgr.Register(monitor.NodeInfo{
		Tag:  "outbound-1",
		Name: node.Name,
		URI:  node.URI,
	})

	mgr := New(&config.Config{}, monitor.Config{}, WithStore(st))
	mgr.monitorMgr = monitorMgr
	mgr.restoreMonitorTrafficFromStore(ctx)

	snapshots := monitorMgr.Snapshot()
	if len(snapshots) != 1 || snapshots[0].TotalUpload != 1000 || snapshots[0].TotalDownload != 2000 {
		t.Fatalf("restored snapshot = %+v", snapshots)
	}

	entry.AddTraffic(300, 400)
	mgr.FlushStatsToStore(ctx)

	stats, err := st.GetNodeStats(ctx, node.ID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.TotalUploadBytes != 1300 || stats.TotalDownloadBytes != 2400 {
		t.Fatalf("flushed stats = %+v", stats)
	}
}
