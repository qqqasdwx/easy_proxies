package boxmgr

import (
	"context"
	"path/filepath"
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
			Source: config.NodeSourceFile,
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

func TestSetNodeEnabledDoesNotRewriteConfigSources(t *testing.T) {
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
		Name:   "inline-node",
		URI:    "http://user:pass@example.com:8080",
		Source: config.NodeSourceInline,
	}
	if err := st.CreateNode(ctx, &store.Node{
		URI:     node.URI,
		Name:    node.Name,
		Source:  store.NodeSourceInline,
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
	if len(nodes) != 1 || nodes[0].Source != config.NodeSourceInline || !nodes[0].Disabled {
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
