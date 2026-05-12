package subscription

import (
	"testing"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

func TestCreateNewConfigUsesCurrentBoxManagerSettings(t *testing.T) {
	baseCfg := &config.Config{
		Mode: "pool",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     23230,
			Protocol: config.InboundProtocolMixed,
		},
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			BasePort: 24030,
			Protocol: config.InboundProtocolMixed,
		},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Timeout: 30 * time.Second,
		},
	}
	currentCfg := &config.Config{
		Mode: "hybrid",
		Listener: config.ListenerConfig{
			Address:  "127.0.0.1",
			Port:     23232,
			Protocol: config.InboundProtocolMixed,
		},
		MultiPort: config.MultiPortConfig{
			Address:  "127.0.0.1",
			BasePort: 24040,
			Protocol: config.InboundProtocolMixed,
		},
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Timeout: 45 * time.Second,
		},
	}

	boxMgr := boxmgr.New(currentCfg, monitor.Config{})
	mgr := New(baseCfg, boxMgr)

	next := mgr.createNewConfig([]config.NodeConfig{{
		Name: "sub-socks",
		URI:  "socks5://127.0.0.1:1080#sub-socks",
	}})

	if next.Mode != "hybrid" {
		t.Fatalf("mode = %q, want hybrid", next.Mode)
	}
	if next.Listener.Port != 23232 {
		t.Fatalf("listener port = %d, want 23232", next.Listener.Port)
	}
	if next.MultiPort.BasePort != 24040 {
		t.Fatalf("multi-port base = %d, want 24040", next.MultiPort.BasePort)
	}
	if next.Nodes[0].Port != 0 {
		t.Fatalf("subscription node port = %d, want opt-in zero port", next.Nodes[0].Port)
	}
	if next.SubscriptionRefresh.Timeout != 45*time.Second {
		t.Fatalf("subscription timeout = %s, want 45s", next.SubscriptionRefresh.Timeout)
	}
	if len(next.Nodes) != 1 || next.Nodes[0].Name != "sub-socks" {
		t.Fatalf("nodes = %+v, want sub-socks", next.Nodes)
	}
}
