package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "nested", "data.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return st
}

func TestOpenMigratesSchema(t *testing.T) {
	st := openTestStore(t)
	sqlite := st.(*sqliteStore)

	version, err := CurrentVersion(sqlite.db)
	if err != nil {
		t.Fatalf("current version: %v", err)
	}
	migrations := allMigrations()
	want := migrations[len(migrations)-1].Version
	if version != want {
		t.Fatalf("schema version = %d, want %d", version, want)
	}
}

func TestStoreNodeCRUDAndBulkUpsert(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	node := &Node{
		URI:     "http://user:pass@example.com:8080",
		Name:    "node-1",
		Source:  NodeSourceManual,
		Port:    24000,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.ID == 0 {
		t.Fatal("created node ID was not populated")
	}

	got, err := st.GetNodeByURI(ctx, node.URI)
	if err != nil {
		t.Fatalf("get node by uri: %v", err)
	}
	if got == nil || got.Name != node.Name || !got.Enabled {
		t.Fatalf("unexpected node: %+v", got)
	}

	got.Name = "node-renamed"
	got.Port = 24001
	if err := st.UpdateNode(ctx, got); err != nil {
		t.Fatalf("update node: %v", err)
	}
	renamed, err := st.GetNode(ctx, got.ID)
	if err != nil {
		t.Fatalf("get updated node: %v", err)
	}
	if renamed.Name != "node-renamed" || renamed.Port != 24001 {
		t.Fatalf("updated node = %+v", renamed)
	}

	if err := st.BulkUpsertNodes(ctx, []Node{
		{
			URI:     node.URI,
			Name:    "node-upserted",
			Source:  NodeSourceManual,
			Port:    25000,
			Enabled: true,
		},
		{
			URI:     "socks5://user:pass@example.net:1080",
			Name:    "node-2",
			Source:  NodeSourceSubscription,
			Enabled: true,
		},
	}); err != nil {
		t.Fatalf("bulk upsert nodes: %v", err)
	}

	count, err := st.CountNodes(ctx, NodeFilter{})
	if err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != 2 {
		t.Fatalf("node count = %d, want 2", count)
	}
	upserted, err := st.GetNodeByURI(ctx, node.URI)
	if err != nil {
		t.Fatalf("get upserted node: %v", err)
	}
	if upserted.Name != "node-upserted" || upserted.Port != 25000 {
		t.Fatalf("upserted node = %+v", upserted)
	}

	deleted, err := st.DeleteNodesBySource(ctx, NodeSourceSubscription)
	if err != nil {
		t.Fatalf("delete nodes by source: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted rows = %d, want 1", deleted)
	}
}

func TestStoreStatsSessionAndSubscriptionStatus(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	node := &Node{
		URI:     "http://user:pass@example.com:8080",
		Name:    "node-1",
		Source:  NodeSourceManual,
		Enabled: true,
	}
	if err := st.CreateNode(ctx, node); err != nil {
		t.Fatalf("create node: %v", err)
	}

	stats := &NodeStats{
		NodeID:             node.ID,
		FailureCount:       2,
		SuccessCount:       5,
		LastLatencyMs:      123,
		Available:          true,
		InitialCheckDone:   true,
		TotalUploadBytes:   1000,
		TotalDownloadBytes: 2000,
	}
	if err := st.UpsertNodeStats(ctx, stats); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}
	gotStats, err := st.GetNodeStats(ctx, node.ID)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if gotStats.SuccessCount != 5 || gotStats.TotalDownloadBytes != 2000 {
		t.Fatalf("unexpected stats: %+v", gotStats)
	}

	now := time.Now().UTC().Truncate(time.Second)
	session := &Session{Token: "token-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	gotSession, err := st.GetSession(ctx, session.Token)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if gotSession == nil || !gotSession.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("unexpected session: %+v", gotSession)
	}
	if err := st.DeleteSession(ctx, session.Token); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	gotSession, err = st.GetSession(ctx, session.Token)
	if err != nil {
		t.Fatalf("get deleted session: %v", err)
	}
	if gotSession != nil {
		t.Fatalf("deleted session still exists: %+v", gotSession)
	}

	status := &SubscriptionStatus{
		LastRefresh:  now,
		NextRefresh:  now.Add(time.Hour),
		NodeCount:    2,
		LastError:    "last error",
		RefreshCount: 3,
		IsRefreshing: true,
		NodesHash:    "hash",
	}
	if err := st.UpdateSubscriptionStatus(ctx, status); err != nil {
		t.Fatalf("update subscription status: %v", err)
	}
	gotStatus, err := st.GetSubscriptionStatus(ctx)
	if err != nil {
		t.Fatalf("get subscription status: %v", err)
	}
	if gotStatus.NodeCount != 2 || gotStatus.RefreshCount != 3 || !gotStatus.IsRefreshing || gotStatus.NodesHash != "hash" {
		t.Fatalf("unexpected subscription status: %+v", gotStatus)
	}
}
