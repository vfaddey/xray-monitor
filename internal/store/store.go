package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/faddey/xray-monitor/internal/model"
	"github.com/faddey/xray-monitor/internal/subscription"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: "_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    port INTEGER NOT NULL,
    first_seen_ms INTEGER NOT NULL,
    last_seen_ms INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    checked_at_ms INTEGER NOT NULL,
    available INTEGER NOT NULL,
    latency_ms INTEGER NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS checks_node_time_idx ON checks(node_id, checked_at_ms DESC);
CREATE INDEX IF NOT EXISTS checks_time_idx ON checks(checked_at_ms);
CREATE TABLE IF NOT EXISTS subscription_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    attempted_at_ms INTEGER NOT NULL,
    successful INTEGER NOT NULL,
    node_count INTEGER NOT NULL,
    error TEXT NOT NULL DEFAULT ''
);
`)
	return err
}

func (s *Store) SyncNodes(ctx context.Context, nodes []subscription.Node, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET active = 0`); err != nil {
		return err
	}
	for _, node := range nodes {
		_, err := tx.ExecContext(ctx, `
INSERT INTO nodes(id, name, address, port, first_seen_ms, last_seen_ms, active)
VALUES(?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    address = excluded.address,
    port = excluded.port,
    last_seen_ms = excluded.last_seen_ms,
    active = 1`, node.ID, node.Name, node.Address, node.Port, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordChecks(ctx context.Context, checks []model.Check) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO checks(node_id, checked_at_ms, available, latency_ms, status_code, error)
VALUES(?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, check := range checks {
		if _, err := stmt.ExecContext(ctx, check.NodeID, check.CheckedAt.UnixMilli(), check.Available, check.LatencyMS, check.StatusCode, check.Error); err != nil {
			return err
		}
	}
	// A bounded retention period prevents an unattended monitor from growing forever.
	if _, err := tx.ExecContext(ctx, `DELETE FROM checks WHERE checked_at_ms < ?`, time.Now().Add(-30*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordSubscription(ctx context.Context, at time.Time, nodeCount int, fetchErr error) error {
	success := fetchErr == nil
	errText := ""
	if fetchErr != nil {
		errText = fetchErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO subscription_events(attempted_at_ms, successful, node_count, error)
VALUES(?, ?, ?, ?)`, at.UnixMilli(), success, nodeCount, errText)
	return err
}

func (s *Store) History(ctx context.Context, nodeID string, limit int) ([]model.Check, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, checked_at_ms, available, latency_ms, status_code, error
FROM checks
WHERE node_id = ?
ORDER BY checked_at_ms DESC
LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Check
	for rows.Next() {
		var check model.Check
		var atMS int64
		if err := rows.Scan(&check.NodeID, &atMS, &check.Available, &check.LatencyMS, &check.StatusCode, &check.Error); err != nil {
			return nil, err
		}
		check.CheckedAt = time.UnixMilli(atMS).UTC()
		result = append(result, check)
	}
	return result, rows.Err()
}
