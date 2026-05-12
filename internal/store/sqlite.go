package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore implements Store using SQLite.
type sqliteStore struct {
	db *sql.DB
	tx *sql.Tx // non-nil when operating inside WithTx
}

// Open creates a new SQLite-backed Store at the given path.
// It applies all pending migrations and sets optimal PRAGMAs.
func Open(dbPath string) (Store, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path is empty")
	}
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory %q: %w", dir, err)
		}
	}

	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)&_pragma=foreign_keys(ON)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(1) // SQLite only supports 1 writer
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0) // connections don't expire

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Run migrations
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("[store] SQLite store opened: %s", dbPath)
	return &sqliteStore{db: db}, nil
}

// conn returns the underlying *sql.Tx or *sql.DB for executing queries.
func (s *sqliteStore) conn() querier {
	if s.tx != nil {
		return s.tx
	}
	return s.db
}

// querier abstracts *sql.DB and *sql.Tx for query execution.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// ===================== Node operations =====================

func (s *sqliteStore) ListNodes(ctx context.Context, filter NodeFilter) ([]Node, error) {
	query := "SELECT id, uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at FROM nodes"
	var conditions []string
	var args []any

	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.SubscriptionID > 0 {
		conditions = append(conditions, "subscription_id = ?")
		args = append(args, filter.SubscriptionID)
	}
	if filter.Region != "" {
		conditions = append(conditions, "region = ?")
		args = append(args, filter.Region)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		if *filter.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id ASC"
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
		if filter.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", filter.Offset)
		}
	}

	rows, err := s.conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	return scanNodes(rows)
}

func (s *sqliteStore) GetNode(ctx context.Context, id int64) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at FROM nodes WHERE id = ?", id)
	return scanNode(row)
}

func (s *sqliteStore) GetNodeByURI(ctx context.Context, uri string) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at FROM nodes WHERE uri = ?", uri)
	return scanNode(row)
}

func (s *sqliteStore) GetNodeByName(ctx context.Context, name string) (*Node, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT id, uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at FROM nodes WHERE name = ?", name)
	return scanNode(row)
}

func (s *sqliteStore) CreateNode(ctx context.Context, node *Node) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now().UTC()
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = time.Now().UTC()
	}
	enabled := 0
	if node.Enabled {
		enabled = 1
	}

	result, err := s.conn().ExecContext(ctx,
		`INSERT INTO nodes (uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.URI, node.Name, node.Source, node.Port,
		node.Username, node.Password, node.SubscriptionID, node.InboundProtocol, node.OutboundJSON, node.Region, node.Country,
		enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	node.ID = id

	// Create initial stats row
	_, err = s.conn().ExecContext(ctx,
		"INSERT OR IGNORE INTO node_stats (node_id) VALUES (?)", id)
	if err != nil {
		return fmt.Errorf("create initial node stats: %w", err)
	}

	return nil
}

func (s *sqliteStore) UpdateNode(ctx context.Context, node *Node) error {
	now := time.Now().UTC().Format(time.RFC3339)
	enabled := 0
	if node.Enabled {
		enabled = 1
	}

	result, err := s.conn().ExecContext(ctx,
		`UPDATE nodes SET uri=?, name=?, source=?, port=?, username=?, password=?,
		 subscription_id=?, inbound_protocol=?, outbound_json=?, region=?, country=?, enabled=?, updated_at=?
		 WHERE id=?`,
		node.URI, node.Name, node.Source, node.Port,
		node.Username, node.Password, node.SubscriptionID, node.InboundProtocol, node.OutboundJSON, node.Region, node.Country,
		enabled, now, node.ID,
	)
	if err != nil {
		return fmt.Errorf("update node %d: %w", node.ID, err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("node %d not found", node.ID)
	}
	return nil
}

func (s *sqliteStore) DeleteNode(ctx context.Context, id int64) error {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("node %d not found", id)
	}
	return nil
}

func (s *sqliteStore) DeleteNodesBySource(ctx context.Context, source string) (int64, error) {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM nodes WHERE source = ?", source)
	if err != nil {
		return 0, fmt.Errorf("delete nodes by source %q: %w", source, err)
	}
	return result.RowsAffected()
}

func (s *sqliteStore) BulkUpsertNodes(ctx context.Context, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	execFn := func(txStore *sqliteStore) error {
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := txStore.conn().PrepareContext(ctx,
			`INSERT INTO nodes (uri, name, source, port, username, password, subscription_id, inbound_protocol, outbound_json, region, country, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(uri) DO UPDATE SET
			   name=excluded.name, source=excluded.source, port=excluded.port,
			   username=excluded.username, password=excluded.password,
			   subscription_id=excluded.subscription_id,
			   inbound_protocol=excluded.inbound_protocol,
			   outbound_json=excluded.outbound_json,
			   region=excluded.region, country=excluded.country,
			   updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("prepare bulk upsert: %w", err)
		}
		defer stmt.Close()

		for i := range nodes {
			n := &nodes[i]
			enabled := 0
			if n.Enabled {
				enabled = 1
			}
			result, err := stmt.ExecContext(ctx,
				n.URI, n.Name, n.Source, n.Port,
				n.Username, n.Password, n.SubscriptionID, n.InboundProtocol, n.OutboundJSON, n.Region, n.Country,
				enabled, now, now,
			)
			if err != nil {
				return fmt.Errorf("upsert node %q: %w", n.URI, err)
			}
			id, _ := result.LastInsertId()
			if id > 0 {
				n.ID = id
			}
		}

		// Create stats rows for new nodes
		_, err = txStore.conn().ExecContext(ctx,
			"INSERT OR IGNORE INTO node_stats (node_id) SELECT id FROM nodes")
		if err != nil {
			return fmt.Errorf("create stats for new nodes: %w", err)
		}

		return nil
	}

	// If already in a transaction, execute directly
	if s.tx != nil {
		return execFn(s)
	}

	// Otherwise wrap in a transaction
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) CountNodes(ctx context.Context, filter NodeFilter) (int64, error) {
	query := "SELECT COUNT(*) FROM nodes"
	var conditions []string
	var args []any

	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, filter.Source)
	}
	if filter.SubscriptionID > 0 {
		conditions = append(conditions, "subscription_id = ?")
		args = append(args, filter.SubscriptionID)
	}
	if filter.Region != "" {
		conditions = append(conditions, "region = ?")
		args = append(args, filter.Region)
	}
	if filter.Enabled != nil {
		conditions = append(conditions, "enabled = ?")
		if *filter.Enabled {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	var count int64
	err := s.conn().QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ===================== Node stats =====================

func (s *sqliteStore) GetNodeStats(ctx context.Context, nodeID int64) (*NodeStats, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms,
		 available, initial_check_done, total_upload_bytes, total_download_bytes, updated_at
		 FROM node_stats WHERE node_id = ?`, nodeID)

	stats := &NodeStats{}
	var blacklistedUntilStr, lastFailureStr, lastSuccessStr, updatedAtStr string
	var blacklisted, available, initialCheckDone int

	err := row.Scan(
		&stats.NodeID, &stats.FailureCount, &stats.SuccessCount,
		&blacklisted, &blacklistedUntilStr,
		&stats.LastError, &lastFailureStr, &lastSuccessStr,
		&stats.LastLatencyMs, &available, &initialCheckDone,
		&stats.TotalUploadBytes, &stats.TotalDownloadBytes, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get node stats %d: %w", nodeID, err)
	}

	stats.Blacklisted = blacklisted != 0
	stats.Available = available != 0
	stats.InitialCheckDone = initialCheckDone != 0
	stats.BlacklistedUntil = parseTime(blacklistedUntilStr)
	stats.LastFailureAt = parseTime(lastFailureStr)
	stats.LastSuccessAt = parseTime(lastSuccessStr)
	stats.UpdatedAt = parseTime(updatedAtStr)

	return stats, nil
}

func (s *sqliteStore) UpsertNodeStats(ctx context.Context, stats *NodeStats) error {
	now := time.Now().UTC().Format(time.RFC3339)
	blacklisted := 0
	if stats.Blacklisted {
		blacklisted = 1
	}
	available := 0
	if stats.Available {
		available = 1
	}
	initialCheckDone := 0
	if stats.InitialCheckDone {
		initialCheckDone = 1
	}

	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO node_stats (node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms, available, initial_check_done,
		 total_upload_bytes, total_download_bytes, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
		   failure_count=excluded.failure_count, success_count=excluded.success_count,
		   blacklisted=excluded.blacklisted, blacklisted_until=excluded.blacklisted_until,
		   last_error=excluded.last_error, last_failure_at=excluded.last_failure_at,
		   last_success_at=excluded.last_success_at, last_latency_ms=excluded.last_latency_ms,
		   available=excluded.available, initial_check_done=excluded.initial_check_done,
		   total_upload_bytes=excluded.total_upload_bytes, total_download_bytes=excluded.total_download_bytes,
		   updated_at=excluded.updated_at`,
		stats.NodeID, stats.FailureCount, stats.SuccessCount,
		blacklisted, formatTime(stats.BlacklistedUntil),
		stats.LastError, formatTime(stats.LastFailureAt), formatTime(stats.LastSuccessAt),
		stats.LastLatencyMs, available, initialCheckDone,
		stats.TotalUploadBytes, stats.TotalDownloadBytes, now,
	)
	return err
}

func (s *sqliteStore) RecordSuccess(ctx context.Context, nodeID int64, latencyMs int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 success_count = success_count + 1,
		 last_success_at = ?,
		 last_latency_ms = ?,
		 available = 1,
		 initial_check_done = 1,
		 updated_at = ?
		 WHERE node_id = ?`,
		now, latencyMs, now, nodeID,
	)
	return err
}

func (s *sqliteStore) RecordFailure(ctx context.Context, nodeID int64, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 failure_count = failure_count + 1,
		 last_error = ?,
		 last_failure_at = ?,
		 updated_at = ?
		 WHERE node_id = ?`,
		errMsg, now, now, nodeID,
	)
	return err
}

func (s *sqliteStore) SetBlacklist(ctx context.Context, nodeID int64, until time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 blacklisted = 1,
		 blacklisted_until = ?,
		 failure_count = 0,
		 updated_at = ?
		 WHERE node_id = ?`,
		formatTime(until), now, nodeID,
	)
	return err
}

func (s *sqliteStore) ClearBlacklist(ctx context.Context, nodeID int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET
		 blacklisted = 0,
		 blacklisted_until = '',
		 updated_at = ?
		 WHERE node_id = ?`,
		now, nodeID,
	)
	return err
}

func (s *sqliteStore) ClearAllBlacklists(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`UPDATE node_stats SET blacklisted = 0, blacklisted_until = '', updated_at = ? WHERE blacklisted = 1`,
		now,
	)
	return err
}

func (s *sqliteStore) BatchUpdateStats(ctx context.Context, updates []StatsUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	execFn := func(txStore *sqliteStore) error {
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := txStore.conn().PrepareContext(ctx,
			`INSERT INTO node_stats (node_id, failure_count, success_count, blacklisted, blacklisted_until,
			 last_error, last_failure_at, last_success_at, last_latency_ms, available, initial_check_done,
			 total_upload_bytes, total_download_bytes, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(node_id) DO UPDATE SET
			   failure_count=excluded.failure_count, success_count=excluded.success_count,
			   blacklisted=excluded.blacklisted, blacklisted_until=excluded.blacklisted_until,
			   last_error=excluded.last_error, last_failure_at=excluded.last_failure_at,
			   last_success_at=excluded.last_success_at, last_latency_ms=excluded.last_latency_ms,
			   available=excluded.available, initial_check_done=excluded.initial_check_done,
			   total_upload_bytes=excluded.total_upload_bytes, total_download_bytes=excluded.total_download_bytes,
			   updated_at=excluded.updated_at`)
		if err != nil {
			return fmt.Errorf("prepare batch stats: %w", err)
		}
		defer stmt.Close()

		for _, u := range updates {
			blacklisted := 0
			if u.Blacklisted {
				blacklisted = 1
			}
			available := 0
			if u.Available {
				available = 1
			}
			initialCheckDone := 0
			if u.InitialCheckDone {
				initialCheckDone = 1
			}

			_, err := stmt.ExecContext(ctx,
				u.NodeID, u.FailureCount, u.SuccessCount,
				blacklisted, formatTime(u.BlacklistedUntil),
				u.LastError, formatTime(u.LastFailureAt), formatTime(u.LastSuccessAt),
				u.LastLatencyMs, available, initialCheckDone,
				u.TotalUploadBytes, u.TotalDownloadBytes, now,
			)
			if err != nil {
				return fmt.Errorf("batch update stats for node %d: %w", u.NodeID, err)
			}
		}
		return nil
	}

	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) GetAllNodeStats(ctx context.Context) (map[int64]*NodeStats, error) {
	rows, err := s.conn().QueryContext(ctx,
		`SELECT node_id, failure_count, success_count, blacklisted, blacklisted_until,
		 last_error, last_failure_at, last_success_at, last_latency_ms,
		 available, initial_check_done, total_upload_bytes, total_download_bytes, updated_at
		 FROM node_stats`)
	if err != nil {
		return nil, fmt.Errorf("get all node stats: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]*NodeStats)
	for rows.Next() {
		stats := &NodeStats{}
		var blacklistedUntilStr, lastFailureStr, lastSuccessStr, updatedAtStr string
		var blacklisted, available, initialCheckDone int

		err := rows.Scan(
			&stats.NodeID, &stats.FailureCount, &stats.SuccessCount,
			&blacklisted, &blacklistedUntilStr,
			&stats.LastError, &lastFailureStr, &lastSuccessStr,
			&stats.LastLatencyMs, &available, &initialCheckDone,
			&stats.TotalUploadBytes, &stats.TotalDownloadBytes, &updatedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan node stats: %w", err)
		}

		stats.Blacklisted = blacklisted != 0
		stats.Available = available != 0
		stats.InitialCheckDone = initialCheckDone != 0
		stats.BlacklistedUntil = parseTime(blacklistedUntilStr)
		stats.LastFailureAt = parseTime(lastFailureStr)
		stats.LastSuccessAt = parseTime(lastSuccessStr)
		stats.UpdatedAt = parseTime(updatedAtStr)

		result[stats.NodeID] = stats
	}
	return result, rows.Err()
}

// ===================== Timeline =====================

func (s *sqliteStore) AppendTimeline(ctx context.Context, nodeID int64, event TimelineEvent) error {
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO node_timeline (node_id, success, latency_ms, error, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		nodeID, boolToInt(event.Success), event.LatencyMs, event.Error,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *sqliteStore) GetTimeline(ctx context.Context, nodeID int64, limit int) ([]TimelineEvent, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.conn().QueryContext(ctx,
		`SELECT id, node_id, success, latency_ms, error, created_at
		 FROM node_timeline WHERE node_id = ?
		 ORDER BY id DESC LIMIT ?`,
		nodeID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get timeline for node %d: %w", nodeID, err)
	}
	defer rows.Close()

	var events []TimelineEvent
	for rows.Next() {
		var evt TimelineEvent
		var success int
		var createdAtStr string
		err := rows.Scan(&evt.ID, &evt.NodeID, &success, &evt.LatencyMs, &evt.Error, &createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("scan timeline event: %w", err)
		}
		evt.Success = success != 0
		evt.CreatedAt = parseTime(createdAtStr)
		events = append(events, evt)
	}

	// Reverse to get chronological order
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, rows.Err()
}

func (s *sqliteStore) CleanupTimeline(ctx context.Context, keepPerNode int) error {
	if keepPerNode <= 0 {
		keepPerNode = 20
	}

	_, err := s.conn().ExecContext(ctx,
		`DELETE FROM node_timeline WHERE id NOT IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY id DESC) as rn
				FROM node_timeline
			) WHERE rn <= ?
		)`, keepPerNode,
	)
	return err
}

// ===================== Sessions =====================

func (s *sqliteStore) CreateSession(ctx context.Context, session *Session) error {
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO sessions (token, created_at, expires_at) VALUES (?, ?, ?)`,
		session.Token,
		session.CreatedAt.UTC().Format(time.RFC3339),
		session.ExpiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *sqliteStore) GetSession(ctx context.Context, token string) (*Session, error) {
	row := s.conn().QueryRowContext(ctx,
		"SELECT token, created_at, expires_at FROM sessions WHERE token = ?", token)

	var sess Session
	var createdAtStr, expiresAtStr string
	err := row.Scan(&sess.Token, &createdAtStr, &expiresAtStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	sess.CreatedAt = parseTime(createdAtStr)
	sess.ExpiresAt = parseTime(expiresAtStr)
	return &sess, nil
}

func (s *sqliteStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.conn().ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	return err
}

func (s *sqliteStore) CleanupExpiredSessions(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now)
	return err
}

// ===================== Subscription status =====================

func (s *sqliteStore) GetSubscriptionStatus(ctx context.Context) (*SubscriptionStatus, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT last_refresh, next_refresh, node_count, last_error,
		 refresh_count, is_refreshing, nodes_hash, updated_at
		 FROM subscription_status WHERE id = 1`)

	var status SubscriptionStatus
	var lastRefreshStr, nextRefreshStr, updatedAtStr string
	var isRefreshing int

	err := row.Scan(
		&lastRefreshStr, &nextRefreshStr, &status.NodeCount,
		&status.LastError, &status.RefreshCount, &isRefreshing,
		&status.NodesHash, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return &SubscriptionStatus{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription status: %w", err)
	}

	status.IsRefreshing = isRefreshing != 0
	status.LastRefresh = parseTime(lastRefreshStr)
	status.NextRefresh = parseTime(nextRefreshStr)
	status.UpdatedAt = parseTime(updatedAtStr)

	return &status, nil
}

func (s *sqliteStore) UpdateSubscriptionStatus(ctx context.Context, status *SubscriptionStatus) error {
	now := time.Now().UTC().Format(time.RFC3339)
	isRefreshing := 0
	if status.IsRefreshing {
		isRefreshing = 1
	}

	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO subscription_status (id, last_refresh, next_refresh, node_count, last_error,
		 refresh_count, is_refreshing, nodes_hash, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   last_refresh=excluded.last_refresh, next_refresh=excluded.next_refresh,
		   node_count=excluded.node_count, last_error=excluded.last_error,
		   refresh_count=excluded.refresh_count, is_refreshing=excluded.is_refreshing,
		   nodes_hash=excluded.nodes_hash, updated_at=excluded.updated_at`,
		formatTime(status.LastRefresh), formatTime(status.NextRefresh),
		status.NodeCount, status.LastError, status.RefreshCount,
		isRefreshing, status.NodesHash, now,
	)
	return err
}

// ===================== App settings =====================

func (s *sqliteStore) GetAppSetting(ctx context.Context, key string) (string, bool, error) {
	row := s.conn().QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key = ?", key)
	var value string
	if err := row.Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get app setting %q: %w", key, err)
	}
	return value, true, nil
}

func (s *sqliteStore) SetAppSetting(ctx context.Context, key string, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.conn().ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, now,
	)
	if err != nil {
		return fmt.Errorf("set app setting %q: %w", key, err)
	}
	return nil
}

// ===================== Subscription sources =====================

func (s *sqliteStore) ListSubscriptionSources(ctx context.Context) ([]SubscriptionSource, error) {
	rows, err := s.conn().QueryContext(ctx,
		`SELECT id, name, url, enabled, auto_update, interval, last_refresh, next_refresh,
		 node_count, last_error, created_at, updated_at
		 FROM subscription_sources ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list subscription sources: %w", err)
	}
	defer rows.Close()

	var sources []SubscriptionSource
	for rows.Next() {
		var src SubscriptionSource
		var enabled, autoUpdate int
		var intervalNanos int64
		var lastRefreshStr, nextRefreshStr, createdAtStr, updatedAtStr string
		if err := rows.Scan(
			&src.ID, &src.Name, &src.URL, &enabled, &autoUpdate, &intervalNanos,
			&lastRefreshStr, &nextRefreshStr, &src.NodeCount, &src.LastError,
			&createdAtStr, &updatedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan subscription source: %w", err)
		}
		src.Enabled = enabled != 0
		src.AutoUpdate = autoUpdate != 0
		src.Interval = time.Duration(intervalNanos)
		src.LastRefresh = parseTime(lastRefreshStr)
		src.NextRefresh = parseTime(nextRefreshStr)
		src.CreatedAt = parseTime(createdAtStr)
		src.UpdatedAt = parseTime(updatedAtStr)
		sources = append(sources, src)
	}
	return sources, rows.Err()
}

func (s *sqliteStore) GetSubscriptionSource(ctx context.Context, id int64) (*SubscriptionSource, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT id, name, url, enabled, auto_update, interval, last_refresh, next_refresh,
		 node_count, last_error, created_at, updated_at
		 FROM subscription_sources WHERE id = ?`, id)
	return scanSubscriptionSource(row)
}

func (s *sqliteStore) CreateSubscriptionSource(ctx context.Context, source *SubscriptionSource) error {
	if source == nil {
		return fmt.Errorf("subscription source is nil")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	name := strings.TrimSpace(source.Name)
	url := strings.TrimSpace(source.URL)
	if url == "" {
		return fmt.Errorf("subscription url is empty")
	}
	interval := source.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	result, err := s.conn().ExecContext(ctx,
		`INSERT INTO subscription_sources
		 (name, url, enabled, auto_update, interval, last_refresh, next_refresh,
		  node_count, last_error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, url, boolToInt(source.Enabled), boolToInt(source.AutoUpdate), int64(interval),
		formatTime(source.LastRefresh), formatTime(source.NextRefresh),
		source.NodeCount, source.LastError, now, now,
	)
	if err != nil {
		return fmt.Errorf("create subscription source: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get subscription source id: %w", err)
	}
	source.ID = id
	source.Name = name
	source.URL = url
	source.Interval = interval
	source.CreatedAt = parseTime(now)
	source.UpdatedAt = parseTime(now)
	return nil
}

func (s *sqliteStore) UpdateSubscriptionSource(ctx context.Context, source *SubscriptionSource) error {
	if source == nil {
		return fmt.Errorf("subscription source is nil")
	}
	if source.ID <= 0 {
		return fmt.Errorf("subscription source id is required")
	}
	url := strings.TrimSpace(source.URL)
	if url == "" {
		return fmt.Errorf("subscription url is empty")
	}
	interval := source.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.conn().ExecContext(ctx,
		`UPDATE subscription_sources
		 SET name=?, url=?, enabled=?, auto_update=?, interval=?,
		     last_refresh=?, next_refresh=?, node_count=?, last_error=?, updated_at=?
		 WHERE id=?`,
		strings.TrimSpace(source.Name), url, boolToInt(source.Enabled), boolToInt(source.AutoUpdate), int64(interval),
		formatTime(source.LastRefresh), formatTime(source.NextRefresh), source.NodeCount, source.LastError, now, source.ID,
	)
	if err != nil {
		return fmt.Errorf("update subscription source %d: %w", source.ID, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("subscription source %d not found", source.ID)
	}
	source.URL = url
	source.Interval = interval
	source.UpdatedAt = parseTime(now)
	return nil
}

func (s *sqliteStore) DeleteSubscriptionSource(ctx context.Context, id int64) error {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM subscription_sources WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete subscription source %d: %w", id, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("subscription source %d not found", id)
	}
	return nil
}

func (s *sqliteStore) ReplaceSubscriptionSources(ctx context.Context, sources []SubscriptionSource) error {
	execFn := func(txStore *sqliteStore) error {
		if _, err := txStore.conn().ExecContext(ctx, "DELETE FROM subscription_sources"); err != nil {
			return fmt.Errorf("clear subscription sources: %w", err)
		}
		if len(sources) == 0 {
			return nil
		}
		now := time.Now().UTC().Format(time.RFC3339)
		stmt, err := txStore.conn().PrepareContext(ctx,
			`INSERT INTO subscription_sources
			 (name, url, enabled, auto_update, interval, last_refresh, next_refresh,
			  node_count, last_error, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare subscription source insert: %w", err)
		}
		defer stmt.Close()

		for i, src := range sources {
			url := strings.TrimSpace(src.URL)
			if url == "" {
				continue
			}
			name := strings.TrimSpace(src.Name)
			if name == "" {
				name = fmt.Sprintf("subscription-%d", i+1)
			}
			interval := src.Interval
			if interval <= 0 {
				interval = time.Hour
			}
			enabled := boolToInt(src.Enabled)
			autoUpdate := boolToInt(src.AutoUpdate)
			if _, err := stmt.ExecContext(ctx,
				name, url, enabled, autoUpdate, int64(interval),
				formatTime(src.LastRefresh), formatTime(src.NextRefresh),
				src.NodeCount, src.LastError, now, now,
			); err != nil {
				return fmt.Errorf("insert subscription source %q: %w", url, err)
			}
		}
		return nil
	}

	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

// ===================== Proxy pools =====================

func (s *sqliteStore) ListProxyPools(ctx context.Context) ([]ProxyPool, error) {
	rows, err := s.conn().QueryContext(ctx,
		`SELECT id, name, enabled, listen_address, listen_port, protocol, username, password,
		 mode, failure_threshold, blacklist_duration, all_nodes, created_at, updated_at
		 FROM proxy_pools ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list proxy pools: %w", err)
	}
	defer rows.Close()

	pools := make([]ProxyPool, 0)
	for rows.Next() {
		pool, err := scanProxyPoolRows(rows)
		if err != nil {
			return nil, err
		}
		pools = append(pools, pool)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for idx := range pools {
		nodeIDs, err := s.listProxyPoolNodeIDs(ctx, pools[idx].ID)
		if err != nil {
			return nil, err
		}
		pools[idx].NodeIDs = nodeIDs
	}
	return pools, nil
}

func (s *sqliteStore) GetProxyPool(ctx context.Context, id int64) (*ProxyPool, error) {
	row := s.conn().QueryRowContext(ctx,
		`SELECT id, name, enabled, listen_address, listen_port, protocol, username, password,
		 mode, failure_threshold, blacklist_duration, all_nodes, created_at, updated_at
		 FROM proxy_pools WHERE id = ?`, id)
	pool, err := scanProxyPool(row)
	if err != nil || pool == nil {
		return pool, err
	}
	nodeIDs, err := s.listProxyPoolNodeIDs(ctx, pool.ID)
	if err != nil {
		return nil, err
	}
	pool.NodeIDs = nodeIDs
	return pool, nil
}

func (s *sqliteStore) CreateProxyPool(ctx context.Context, pool *ProxyPool) error {
	if pool == nil {
		return fmt.Errorf("proxy pool is nil")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if pool.BlacklistDuration <= 0 {
		pool.BlacklistDuration = 24 * time.Hour
	}
	execFn := func(txStore *sqliteStore) error {
		result, err := txStore.conn().ExecContext(ctx,
			`INSERT INTO proxy_pools
			 (name, enabled, listen_address, listen_port, protocol, username, password,
			  mode, failure_threshold, blacklist_duration, all_nodes, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			strings.TrimSpace(pool.Name), boolToInt(pool.Enabled), strings.TrimSpace(pool.ListenAddress), pool.ListenPort,
			strings.TrimSpace(pool.Protocol), pool.Username, pool.Password, strings.TrimSpace(pool.Mode),
			pool.FailureThreshold, int64(pool.BlacklistDuration), boolToInt(pool.AllNodes), now, now,
		)
		if err != nil {
			return fmt.Errorf("create proxy pool: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("get proxy pool id: %w", err)
		}
		pool.ID = id
		pool.CreatedAt = parseTime(now)
		pool.UpdatedAt = parseTime(now)
		return txStore.replaceProxyPoolNodes(ctx, pool.ID, pool.NodeIDs)
	}
	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) UpdateProxyPool(ctx context.Context, pool *ProxyPool) error {
	if pool == nil {
		return fmt.Errorf("proxy pool is nil")
	}
	if pool.ID <= 0 {
		return fmt.Errorf("proxy pool id is required")
	}
	if pool.BlacklistDuration <= 0 {
		pool.BlacklistDuration = 24 * time.Hour
	}
	now := time.Now().UTC().Format(time.RFC3339)
	execFn := func(txStore *sqliteStore) error {
		result, err := txStore.conn().ExecContext(ctx,
			`UPDATE proxy_pools
			 SET name=?, enabled=?, listen_address=?, listen_port=?, protocol=?, username=?, password=?,
			     mode=?, failure_threshold=?, blacklist_duration=?, all_nodes=?, updated_at=?
			 WHERE id=?`,
			strings.TrimSpace(pool.Name), boolToInt(pool.Enabled), strings.TrimSpace(pool.ListenAddress), pool.ListenPort,
			strings.TrimSpace(pool.Protocol), pool.Username, pool.Password, strings.TrimSpace(pool.Mode),
			pool.FailureThreshold, int64(pool.BlacklistDuration), boolToInt(pool.AllNodes), now, pool.ID,
		)
		if err != nil {
			return fmt.Errorf("update proxy pool %d: %w", pool.ID, err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			return fmt.Errorf("proxy pool %d not found", pool.ID)
		}
		pool.UpdatedAt = parseTime(now)
		return txStore.replaceProxyPoolNodes(ctx, pool.ID, pool.NodeIDs)
	}
	if s.tx != nil {
		return execFn(s)
	}
	return s.WithTx(ctx, func(tx Store) error {
		return execFn(tx.(*sqliteStore))
	})
}

func (s *sqliteStore) DeleteProxyPool(ctx context.Context, id int64) error {
	result, err := s.conn().ExecContext(ctx, "DELETE FROM proxy_pools WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete proxy pool %d: %w", id, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("proxy pool %d not found", id)
	}
	return nil
}

func (s *sqliteStore) listProxyPoolNodeIDs(ctx context.Context, poolID int64) ([]int64, error) {
	rows, err := s.conn().QueryContext(ctx, "SELECT node_id FROM proxy_pool_nodes WHERE pool_id = ? ORDER BY node_id ASC", poolID)
	if err != nil {
		return nil, fmt.Errorf("list proxy pool nodes: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *sqliteStore) replaceProxyPoolNodes(ctx context.Context, poolID int64, nodeIDs []int64) error {
	if _, err := s.conn().ExecContext(ctx, "DELETE FROM proxy_pool_nodes WHERE pool_id = ?", poolID); err != nil {
		return fmt.Errorf("clear proxy pool nodes: %w", err)
	}
	if len(nodeIDs) == 0 {
		return nil
	}
	stmt, err := s.conn().PrepareContext(ctx, "INSERT OR IGNORE INTO proxy_pool_nodes (pool_id, node_id) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("prepare proxy pool node insert: %w", err)
	}
	defer stmt.Close()
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, poolID, nodeID); err != nil {
			return fmt.Errorf("insert proxy pool node %d: %w", nodeID, err)
		}
	}
	return nil
}

// ===================== Lifecycle =====================

func (s *sqliteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *sqliteStore) WithTx(ctx context.Context, fn func(tx Store) error) error {
	if s.tx != nil {
		// Already in a transaction, just execute
		return fn(s)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txStore := &sqliteStore{db: s.db, tx: tx}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ===================== Helpers =====================

func scanNode(row *sql.Row) (*Node, error) {
	var n Node
	var enabled int
	var createdAtStr, updatedAtStr string

	err := row.Scan(
		&n.ID, &n.URI, &n.Name, &n.Source, &n.Port,
		&n.Username, &n.Password, &n.SubscriptionID, &n.InboundProtocol, &n.OutboundJSON, &n.Region, &n.Country,
		&enabled, &createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	n.Enabled = enabled != 0
	n.CreatedAt = parseTime(createdAtStr)
	n.UpdatedAt = parseTime(updatedAtStr)
	return &n, nil
}

func scanNodes(rows *sql.Rows) ([]Node, error) {
	var nodes []Node
	for rows.Next() {
		var n Node
		var enabled int
		var createdAtStr, updatedAtStr string

		err := rows.Scan(
			&n.ID, &n.URI, &n.Name, &n.Source, &n.Port,
			&n.Username, &n.Password, &n.SubscriptionID, &n.InboundProtocol, &n.OutboundJSON, &n.Region, &n.Country,
			&enabled, &createdAtStr, &updatedAtStr,
		)
		if err != nil {
			return nil, err
		}

		n.Enabled = enabled != 0
		n.CreatedAt = parseTime(createdAtStr)
		n.UpdatedAt = parseTime(updatedAtStr)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

func scanSubscriptionSource(row *sql.Row) (*SubscriptionSource, error) {
	var src SubscriptionSource
	var enabled, autoUpdate int
	var intervalNanos int64
	var lastRefreshStr, nextRefreshStr, createdAtStr, updatedAtStr string
	err := row.Scan(
		&src.ID, &src.Name, &src.URL, &enabled, &autoUpdate, &intervalNanos,
		&lastRefreshStr, &nextRefreshStr, &src.NodeCount, &src.LastError,
		&createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan subscription source: %w", err)
	}
	src.Enabled = enabled != 0
	src.AutoUpdate = autoUpdate != 0
	src.Interval = time.Duration(intervalNanos)
	src.LastRefresh = parseTime(lastRefreshStr)
	src.NextRefresh = parseTime(nextRefreshStr)
	src.CreatedAt = parseTime(createdAtStr)
	src.UpdatedAt = parseTime(updatedAtStr)
	return &src, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanProxyPool(row scanner) (*ProxyPool, error) {
	var pool ProxyPool
	var enabled, allNodes int
	var durationNanos int64
	var createdAtStr, updatedAtStr string
	err := row.Scan(
		&pool.ID, &pool.Name, &enabled, &pool.ListenAddress, &pool.ListenPort, &pool.Protocol,
		&pool.Username, &pool.Password, &pool.Mode, &pool.FailureThreshold, &durationNanos,
		&allNodes, &createdAtStr, &updatedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan proxy pool: %w", err)
	}
	pool.Enabled = enabled != 0
	pool.AllNodes = allNodes != 0
	pool.BlacklistDuration = time.Duration(durationNanos)
	pool.CreatedAt = parseTime(createdAtStr)
	pool.UpdatedAt = parseTime(updatedAtStr)
	return &pool, nil
}

func scanProxyPoolRows(rows *sql.Rows) (ProxyPool, error) {
	pool, err := scanProxyPool(rows)
	if err != nil {
		return ProxyPool{}, err
	}
	if pool == nil {
		return ProxyPool{}, nil
	}
	return *pool, nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try other formats
		t, err = time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
