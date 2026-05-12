package monitor

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

func TestHandleProxyPoolsRejectsUnknownNodeIDs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	server := &Server{store: st, cfgSrc: &config.Config{}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"custom",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":2324,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":false,
		"node_ids":[999]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("节点 999 不存在")) {
		t.Fatalf("body = %s, want missing node error", rec.Body.String())
	}
}

func TestHandleProxyPoolsPersistsKnownNodeIDs(t *testing.T) {
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
		URI:     "http://user:pass@example.com:8080",
		Name:    "node-1",
		Source:  store.NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(t.Context(), node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	server := &Server{store: st, cfgSrc: &config.Config{}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"custom",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":2324,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":false,
		"node_ids":[` + jsonNumber(node.ID) + `]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ProxyPool struct {
			NodeIDs []int64 `json:"node_ids"`
		} `json:"proxy_pool"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.ProxyPool.NodeIDs) != 1 || resp.ProxyPool.NodeIDs[0] != node.ID {
		t.Fatalf("node_ids = %+v, want [%d]", resp.ProxyPool.NodeIDs, node.ID)
	}
}

func TestHandleProxyPoolsRejectsNodePortConflict(t *testing.T) {
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
		URI:     "http://user:pass@port.example.com:8080",
		Name:    "node-port",
		Source:  store.NodeSourceManual,
		Port:    24000,
		Enabled: true,
	}
	if err := st.CreateNode(t.Context(), node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	server := &Server{store: st, cfgSrc: &config.Config{}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"port-conflict",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":24000,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("监听端口 24000 已被节点")) {
		t.Fatalf("body = %s, want node port conflict", rec.Body.String())
	}
}

func TestHandleProxyPoolsRejectsDuplicateListenPort(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	server := &Server{store: st, cfgSrc: &config.Config{}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"duplicate",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":2323,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("监听端口 2323 已被代理池")) {
		t.Fatalf("body = %s, want proxy pool port conflict", rec.Body.String())
	}
}

func TestHandleProxyPoolsRejectsGeoIPPortConflict(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	server := &Server{store: st, cfgSrc: &config.Config{
		GeoIP: config.GeoIPConfig{Enabled: true, Port: 1221},
	}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"geoip-conflict",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":1221,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("监听端口 1221 已被 GeoIP 路由使用")) {
		t.Fatalf("body = %s, want geoip port conflict", rec.Body.String())
	}
}

func TestHandleProxyPoolsRejectsManagementPortConflict(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	server := &Server{store: st, cfgSrc: &config.Config{
		Management: config.ManagementConfig{Listen: "0.0.0.0:9091"},
	}, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"name":"management-conflict",
		"enabled":true,
		"listen_address":"127.0.0.1",
		"listen_port":9091,
		"protocol":"mixed",
		"mode":"sequential",
		"failure_threshold":3,
		"blacklist_duration":"24h",
		"all_nodes":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", body)
	rec := httptest.NewRecorder()

	server.handleProxyPools(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("监听端口 9091 已被管理面板使用")) {
		t.Fatalf("body = %s, want management port conflict", rec.Body.String())
	}
}

func TestHandleProxyPoolDeleteRejectsLastPool(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	pools, err := st.ListProxyPools(t.Context())
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("default pools = %d, want 1", len(pools))
	}

	server := &Server{store: st, cfgSrc: &config.Config{}, logger: log.Default()}
	req := httptest.NewRequest(http.MethodDelete, "/api/proxy-pools/"+jsonNumber(pools[0].ID), nil)
	rec := httptest.NewRecorder()

	server.handleProxyPoolItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("至少需要保留一个代理池")) {
		t.Fatalf("body = %s, want last pool error", rec.Body.String())
	}
}

func TestMonitorProxyCredentialsSkipsDisabledProxyPool(t *testing.T) {
	username, password := monitorProxyCredentials(&config.Config{
		ProxyPools: []config.ProxyPoolConfig{
			{
				Name:    "disabled",
				Enabled: false,
				Listener: config.ListenerConfig{
					Username: "disabled-user",
					Password: "disabled-pass",
				},
			},
			{
				Name:    "enabled",
				Enabled: true,
				Listener: config.ListenerConfig{
					Username: "enabled-user",
					Password: "enabled-pass",
				},
			},
		},
	})
	if username != "enabled-user" || password != "enabled-pass" {
		t.Fatalf("credentials = %q/%q, want enabled pool credentials", username, password)
	}
}

func TestMonitorProxyCredentialsSkipsUnauthenticatedEnabledProxyPool(t *testing.T) {
	username, password := monitorProxyCredentials(&config.Config{
		MultiPort: config.MultiPortConfig{Username: "multi-user", Password: "multi-pass"},
		ProxyPools: []config.ProxyPoolConfig{
			{
				Name:    "public",
				Enabled: true,
			},
			{
				Name:    "authenticated",
				Enabled: true,
				Listener: config.ListenerConfig{
					Username: "pool-user",
					Password: "pool-pass",
				},
			},
		},
	})
	if username != "pool-user" || password != "pool-pass" {
		t.Fatalf("credentials = %q/%q, want authenticated pool credentials", username, password)
	}
}

func TestHandleExportGeoIPUsesAuthenticatedPoolCredentials(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(mgr.Stop)

	server := &Server{
		mgr: mgr,
		cfgSrc: &config.Config{
			GeoIP: config.GeoIPConfig{Enabled: true, Listen: "127.0.0.1", Port: 1221},
			ProxyPools: []config.ProxyPoolConfig{
				{
					ID:      1,
					Name:    "public",
					Enabled: true,
					Listener: config.ListenerConfig{
						Address:  "127.0.0.1",
						Port:     2323,
						Protocol: config.InboundProtocolHTTP,
					},
				},
				{
					ID:      2,
					Name:    "authenticated",
					Enabled: true,
					Listener: config.ListenerConfig{
						Address:  "127.0.0.1",
						Port:     2324,
						Protocol: config.InboundProtocolHTTP,
						Username: "pool-user",
						Password: "pool-pass",
					},
				},
			},
		},
		logger: log.Default(),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/export?scheme=http", nil)
	rec := httptest.NewRecorder()

	server.handleExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("http://pool-user:pool-pass@127.0.0.1:1221")) {
		t.Fatalf("body = %s, want authenticated GeoIP export URI", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("http://127.0.0.1:1221")) {
		t.Fatalf("body = %s, should not include unauthenticated GeoIP URI", rec.Body.String())
	}
}

func TestHandleExportGeoIPFallsBackToProxyPoolListenAddress(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(mgr.Stop)

	server := &Server{
		mgr: mgr,
		cfgSrc: &config.Config{
			GeoIP: config.GeoIPConfig{Enabled: true, Port: 1221},
			ProxyPools: []config.ProxyPoolConfig{{
				ID:      1,
				Name:    "default",
				Enabled: true,
				Listener: config.ListenerConfig{
					Address:  "127.0.0.1",
					Port:     2323,
					Protocol: config.InboundProtocolHTTP,
				},
			}},
		},
		logger: log.Default(),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/export?scheme=http", nil)
	rec := httptest.NewRecorder()

	server.handleExport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("http://127.0.0.1:1221")) {
		t.Fatalf("body = %s, want GeoIP URI to use proxy pool listen address", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("http://:1221")) {
		t.Fatalf("body = %s, should not include empty-host GeoIP URI", rec.Body.String())
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
