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
