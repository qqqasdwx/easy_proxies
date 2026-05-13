package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrafficSummaryAndSpeedSampling(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.Stop()

	nodeB := mgr.Register(NodeInfo{Tag: "node-b", Name: "B"})
	nodeA := mgr.Register(NodeInfo{Tag: "node-a", Name: "A"})

	nodeB.AddTraffic(100, 200)
	nodeA.AddTraffic(1000, 2000)
	t0 := time.Unix(100, 0)
	mgr.sampleTrafficSpeeds(t0)

	nodeB.AddTraffic(50, 60)
	nodeA.AddTraffic(500, 1500)
	mgr.sampleTrafficSpeeds(t0.Add(2 * time.Second))

	summary := mgr.TrafficSummary(true)
	if summary.NodeCount != 2 {
		t.Fatalf("node count = %d, want 2", summary.NodeCount)
	}
	if summary.TotalUpload != 1650 || summary.TotalDownload != 3760 {
		t.Fatalf("totals = up %d down %d", summary.TotalUpload, summary.TotalDownload)
	}
	if summary.UploadSpeed != 275 || summary.DownloadSpeed != 780 {
		t.Fatalf("speeds = up %d down %d", summary.UploadSpeed, summary.DownloadSpeed)
	}
	if len(summary.Nodes) != 2 || summary.Nodes[0].Tag != "node-a" || summary.Nodes[1].Tag != "node-b" {
		t.Fatalf("nodes were not sorted by tag: %+v", summary.Nodes)
	}
}

func TestSetTrafficRestoresTotals(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.Stop()

	node := mgr.Register(NodeInfo{Tag: "node-a", Name: "A"})
	node.SetTraffic(-1, 4096)
	node.AddTraffic(512, 128)

	snapshots := mgr.Snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	if snapshots[0].TotalUpload != 512 || snapshots[0].TotalDownload != 4224 {
		t.Fatalf("restored totals = up %d down %d", snapshots[0].TotalUpload, snapshots[0].TotalDownload)
	}
}

func TestUpdateConfigRefreshesProbeDestination(t *testing.T) {
	mgr, err := NewManager(Config{ProbeTarget: "https://example.com/generate_204"})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	defer mgr.Stop()

	initial, ok := mgr.DestinationForProbe()
	if !ok || initial.String() != "example.com:443" {
		t.Fatalf("initial probe destination = %q, %v; want example.com:443", initial.String(), ok)
	}

	mgr.UpdateConfig(Config{ProbeTarget: "http://127.0.0.1:18080/generate_204"})
	updated, ok := mgr.DestinationForProbe()
	if !ok || updated.String() != "127.0.0.1:18080" {
		t.Fatalf("updated probe destination = %q, %v; want 127.0.0.1:18080", updated.String(), ok)
	}
}

func TestHandleTrafficStreamInitialEvent(t *testing.T) {
	mgr, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.Stop()
	node := mgr.Register(NodeInfo{Tag: "node-a", Name: "A"})
	node.AddTraffic(123, 456)

	server := NewServer(Config{Enabled: true, Listen: "127.0.0.1:0"}, mgr, log.Default())
	testServer := httptest.NewServer(server.srv.Handler)
	defer testServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/api/nodes/traffic/stream", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		cancel()
		var payload struct {
			Type          string `json:"type"`
			NodeCount     int    `json:"node_count"`
			TotalUpload   int64  `json:"total_upload"`
			TotalDownload int64  `json:"total_download"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		if payload.Type != "traffic" || payload.NodeCount != 1 || payload.TotalUpload != 123 || payload.TotalDownload != 456 {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		return
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan event: %v", err)
	}
	t.Fatal("stream ended before data event")
}
