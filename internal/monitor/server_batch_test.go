package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

type fakeNodeManager struct {
	nodes   map[string]config.NodeConfig
	enabled map[string]bool
}

func newFakeNodeManager(nodes ...config.NodeConfig) *fakeNodeManager {
	m := &fakeNodeManager{
		nodes:   make(map[string]config.NodeConfig),
		enabled: make(map[string]bool),
	}
	for _, node := range nodes {
		m.nodes[node.Name] = node
		m.enabled[node.Name] = !node.Disabled
	}
	return m
}

func (m *fakeNodeManager) ListConfigNodes(context.Context) ([]config.NodeConfig, error) {
	nodes := make([]config.NodeConfig, 0, len(m.nodes))
	for _, node := range m.nodes {
		node.Disabled = !m.enabled[node.Name]
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (m *fakeNodeManager) ParseNodeURI(_ context.Context, name string, uri string) (config.NodeConfig, error) {
	name = strings.TrimSpace(name)
	uri = strings.TrimSpace(uri)
	if name == "" {
		name = config.ExtractNodeName(uri)
	}
	if name == "" {
		name = "node"
	}
	return config.NodeConfig{
		Name:         name,
		URI:          uri,
		OutboundJSON: `{"type":"socks","tag":"` + name + `","server":"127.0.0.1","server_port":1080}`,
	}, nil
}

func (m *fakeNodeManager) CreateNode(_ context.Context, node config.NodeConfig) (config.NodeConfig, error) {
	if node.Name == "" {
		node.Name = config.ExtractNodeName(node.URI)
	}
	if node.Name == "" {
		node.Name = "imported"
	}
	if _, ok := m.nodes[node.Name]; ok {
		return config.NodeConfig{}, ErrNodeConflict
	}
	m.nodes[node.Name] = node
	m.enabled[node.Name] = !node.Disabled
	return node, nil
}

func (m *fakeNodeManager) UpdateNode(_ context.Context, name string, node config.NodeConfig) (config.NodeConfig, error) {
	if _, ok := m.nodes[name]; !ok {
		return config.NodeConfig{}, ErrNodeNotFound
	}
	m.nodes[name] = node
	m.enabled[name] = !node.Disabled
	return node, nil
}

func (m *fakeNodeManager) SetNodeEnabled(_ context.Context, name string, enabled bool) error {
	if _, ok := m.nodes[name]; !ok {
		return ErrNodeNotFound
	}
	m.enabled[name] = enabled
	return nil
}

func (m *fakeNodeManager) DeleteNode(_ context.Context, name string) error {
	if _, ok := m.nodes[name]; !ok {
		return ErrNodeNotFound
	}
	delete(m.nodes, name)
	delete(m.enabled, name)
	return nil
}

func (m *fakeNodeManager) TriggerReload(context.Context) error {
	return nil
}

func TestHandleConfigNodesBatchToggle(t *testing.T) {
	server := &Server{nodeMgr: newFakeNodeManager(config.NodeConfig{Name: "node-1"}), logger: log.Default()}
	body := bytes.NewBufferString(`{"names":["node-1"],"enabled":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-toggle", body)
	rec := httptest.NewRecorder()

	server.handleConfigNodesBatchToggle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["success"].(float64) != 1 || resp["need_reload"] != true {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if server.nodeMgr.(*fakeNodeManager).enabled["node-1"] {
		t.Fatal("node was not disabled")
	}
}

func TestHandleConfigNodesBatchDeletePartialFailure(t *testing.T) {
	server := &Server{nodeMgr: newFakeNodeManager(config.NodeConfig{Name: "node-1"}), logger: log.Default()}
	body := bytes.NewBufferString(`{"names":["node-1","missing"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/batch-delete", body)
	rec := httptest.NewRecorder()

	server.handleConfigNodesBatchDelete(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Success int      `json:"success"`
		Total   int      `json:"total"`
		Errors  []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Success != 1 || resp.Total != 2 || len(resp.Errors) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandleImportReportsInvalidLines(t *testing.T) {
	server := &Server{nodeMgr: newFakeNodeManager(), logger: log.Default()}
	body := bytes.NewBufferString(`{"content":"not-a-uri\nhttp://user:pass@example.com:8080#imported"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	rec := httptest.NewRecorder()

	server.handleImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Imported int      `json:"imported"`
		Errors   []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Imported != 1 || len(resp.Errors) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandleNodeURIParseReturnsOutboundJSON(t *testing.T) {
	server := &Server{nodeMgr: newFakeNodeManager(), logger: log.Default()}
	body := bytes.NewBufferString(`{"uri":"socks5://user:pass@127.0.0.1:1080#parsed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/nodes/config/parse-uri", body)
	rec := httptest.NewRecorder()

	server.handleNodeURIParse(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Name         string `json:"name"`
		URI          string `json:"uri"`
		OutboundJSON string `json:"outbound_json"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Name != "parsed" || !strings.Contains(resp.OutboundJSON, `"server":"127.0.0.1"`) {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestRespondNodeErrorStillHandlesWrappedSentinels(t *testing.T) {
	server := &Server{logger: log.Default()}
	rec := httptest.NewRecorder()

	server.respondNodeError(rec, errors.Join(ErrNodeNotFound))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleSettingsRejectsInvalidValuesWithoutMutatingConfig(t *testing.T) {
	cfg := &config.Config{
		Mode:           "pool",
		LogLevel:       "info",
		Listener:       config.ListenerConfig{Address: "0.0.0.0", Port: 2323, Protocol: config.InboundProtocolMixed},
		MultiPort:      config.MultiPortConfig{Address: "0.0.0.0", BasePort: 24000, Protocol: config.InboundProtocolMixed},
		Pool:           config.PoolConfig{Mode: "sequential", FailureThreshold: 3, BlacklistDuration: 24 * time.Hour},
		Management:     config.ManagementConfig{ProbeTarget: "www.apple.com:80"},
		GeoIP:          config.GeoIPConfig{AutoUpdateInterval: 24 * time.Hour},
		HealthCheck:    config.HealthCheckConfig{Interval: 5 * time.Minute, Timeout: 10 * time.Second, Concurrency: 8},
		DNS:            config.DNSConfig{Strategy: config.DNSStrategyPreferIPv4},
		SkipCertVerify: false,
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Interval:           time.Hour,
			Timeout:            30 * time.Second,
			HealthCheckTimeout: time.Minute,
			DrainTimeout:       30 * time.Second,
			MinAvailableNodes:  1,
		},
	}
	server := &Server{cfgSrc: cfg, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"mode":"invalid",
		"log_level":"verbose",
		"pool":{"mode":"least_conn","failure_threshold":3,"blacklist_duration":"bad"},
		"geoip":{"enabled":true,"auto_update_interval":"bad"},
		"health_check":{"interval":"0s","timeout":"10s","concurrency":8}
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	rec := httptest.NewRecorder()

	server.handleSettings(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cfg.Mode != "pool" || cfg.LogLevel != "info" || cfg.Pool.Mode != "sequential" || cfg.GeoIP.Enabled {
		t.Fatalf("config mutated after invalid request: %+v", cfg)
	}
}

func TestHandleSettingsPreservesGeoIPWhenOmitted(t *testing.T) {
	cfg := &config.Config{
		Mode:           "pool",
		LogLevel:       "info",
		Listener:       config.ListenerConfig{Address: "0.0.0.0", Port: 2323, Protocol: config.InboundProtocolMixed},
		MultiPort:      config.MultiPortConfig{Address: "0.0.0.0", BasePort: 24000, Protocol: config.InboundProtocolMixed},
		Pool:           config.PoolConfig{Mode: "sequential", FailureThreshold: 3, BlacklistDuration: 24 * time.Hour},
		Management:     config.ManagementConfig{ProbeTarget: "www.apple.com:80"},
		GeoIP:          config.GeoIPConfig{Enabled: true, Port: 1221, AutoUpdateInterval: 24 * time.Hour},
		HealthCheck:    config.HealthCheckConfig{Interval: 5 * time.Minute, Timeout: 10 * time.Second, Concurrency: 8},
		DNS:            config.DNSConfig{Strategy: config.DNSStrategyPreferIPv4},
		SkipCertVerify: false,
		SubscriptionRefresh: config.SubscriptionRefreshConfig{
			Interval:           time.Hour,
			Timeout:            30 * time.Second,
			HealthCheckTimeout: time.Minute,
			DrainTimeout:       30 * time.Second,
			MinAvailableNodes:  1,
		},
	}
	server := &Server{cfgSrc: cfg, logger: log.Default()}
	body := bytes.NewBufferString(`{
		"external_ip":"",
		"probe_target":"www.apple.com:80",
		"log_level":"info",
		"skip_cert_verify":false
	}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	rec := httptest.NewRecorder()

	server.handleSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !cfg.GeoIP.Enabled || cfg.GeoIP.Port != 1221 {
		t.Fatalf("geoip config = %+v, want enabled port 1221", cfg.GeoIP)
	}
}

func TestHandleAuthLogoutDeletesSessionAndClearsCookie(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})

	server := &Server{
		cfg:        Config{Password: "secret"},
		store:      st,
		logger:     log.Default(),
		sessionTTL: time.Hour,
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth", bytes.NewBufferString(`{"password":"secret"}`))
	loginRec := httptest.NewRecorder()
	server.handleAuth(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRec.Code, loginRec.Body.String())
	}
	if strings.Contains(loginRec.Body.String(), "token") {
		t.Fatalf("login response exposed session token: %s", loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("unexpected login cookies: %+v", cookies)
	}
	token := cookies[0].Value
	if sess, err := st.GetSession(context.Background(), token); err != nil || sess == nil {
		t.Fatalf("session was not stored: session=%+v err=%v", sess, err)
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/api/auth", nil)
	logoutReq.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	server.handleAuth(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", logoutRec.Code, logoutRec.Body.String())
	}
	if sess, err := st.GetSession(context.Background(), token); err != nil || sess != nil {
		t.Fatalf("session was not deleted: session=%+v err=%v", sess, err)
	}
	cleared := logoutRec.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != sessionCookieName || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout did not clear cookie: %+v", cleared)
	}
}
