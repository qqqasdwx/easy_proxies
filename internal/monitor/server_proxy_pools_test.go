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

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
