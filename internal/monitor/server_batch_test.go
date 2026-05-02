package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"easy_proxies/internal/config"
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

func TestRespondNodeErrorStillHandlesWrappedSentinels(t *testing.T) {
	server := &Server{logger: log.Default()}
	rec := httptest.NewRecorder()

	server.respondNodeError(rec, errors.Join(ErrNodeNotFound))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
