package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func (s *Store) migrateSessionStacks(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS session_stacks (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
 region TEXT NOT NULL, stack_name TEXT NOT NULL, stack_slug TEXT NOT NULL,
 stack_url TEXT NOT NULL DEFAULT '', otlp_endpoint TEXT NOT NULL DEFAULT '', instance_id TEXT NOT NULL DEFAULT '',
status TEXT NOT NULL CHECK(status IN ('provisioning','ready','failed','awaiting_auth','needs_auth')),
 progress TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
UPDATE session_stacks SET status = 'failed', error = 'Stack provisioning was interrupted by a server restart; retry to recover the stack', updated_at = ? WHERE status = 'provisioning';
INSERT OR IGNORE INTO schema_migrations(version) VALUES (12);
`, formatTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 13`).Scan(&applied); err != nil {
		return err
	}
	if applied == 0 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `
ALTER TABLE session_stacks RENAME TO session_stacks_old;
CREATE TABLE session_stacks (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL UNIQUE REFERENCES sessions(id) ON DELETE CASCADE,
 region TEXT NOT NULL, stack_name TEXT NOT NULL, stack_slug TEXT NOT NULL,
 stack_url TEXT NOT NULL DEFAULT '', otlp_endpoint TEXT NOT NULL DEFAULT '', instance_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('provisioning','ready','failed','awaiting_auth','needs_auth')),
 progress TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
INSERT INTO session_stacks SELECT id, session_id, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id,
 CASE WHEN status = 'ready' THEN 'needs_auth' ELSE status END, progress, error, created_at, updated_at FROM session_stacks_old;
DROP TABLE session_stacks_old;
INSERT INTO schema_migrations(version) VALUES (13);
`)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO session_stacks
 (id, session_id, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, created_at, updated_at)
 SELECT id, session_id, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, 'needs_auth', '[]', created_at, updated_at
 FROM deployments WHERE stack_url <> '' AND stack_slug <> '' ORDER BY created_at DESC;
UPDATE session_stacks SET status = 'needs_auth', error = 'Connection interrupted by a server restart; connect Grafana to retry', updated_at = ? WHERE status = 'awaiting_auth'`, formatTime(time.Now().UTC()))
	return err
}

// ClaimStackConnect serializes authentication with creation and deletion.
func (s *Store) ClaimStackConnect(ctx context.Context, sessionID string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE session_stacks SET status = 'awaiting_auth', error = '', updated_at = ?
 WHERE session_id = ? AND stack_url <> '' AND status IN ('needs_auth','ready')
 AND NOT EXISTS (SELECT 1 FROM session_deletions WHERE session_id = ?)`, formatTime(time.Now().UTC()), sessionID, sessionID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// ClaimGrafanaStack atomically inserts or retries a failed stack. A session can
// have only one stack; retries retain its identity so provisioning can recover it.
func (s *Store) ClaimGrafanaStack(ctx context.Context, item domain.GrafanaStack) (domain.GrafanaStack, bool, error) {
	now := time.Now().UTC()
	item.ID, item.CreatedAt, item.UpdatedAt = newID(), now, now
	if item.Progress == nil {
		item.Progress = []string{}
	}
	progress, err := json.Marshal(item.Progress)
	if err != nil {
		return item, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO session_stacks
 (id, session_id, region, stack_name, stack_slug, status, progress, created_at, updated_at)
 SELECT ?, ?, ?, ?, ?, 'provisioning', ?, ?, ?
 WHERE NOT EXISTS (SELECT 1 FROM session_deletions WHERE session_id = ?)
 ON CONFLICT(session_id) DO UPDATE SET status = 'provisioning', region = CASE WHEN session_stacks.stack_url = '' THEN excluded.region ELSE session_stacks.region END, progress = excluded.progress, error = '', updated_at = excluded.updated_at
 WHERE session_stacks.status = 'failed'`, item.ID, item.SessionID, item.Region, item.StackName, item.StackSlug, string(progress), formatTime(now), formatTime(now), item.SessionID)
	if err != nil {
		return item, false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return item, false, err
	}
	saved, err := s.GetGrafanaStack(ctx, item.SessionID)
	if err != nil {
		return item, false, err
	}
	return *saved, true, nil
}

func (s *Store) GetGrafanaStack(ctx context.Context, sessionID string) (*domain.GrafanaStack, error) {
	var item domain.GrafanaStack
	var progress, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, error, created_at, updated_at FROM session_stacks WHERE session_id = ?`, sessionID).Scan(
		&item.ID, &item.SessionID, &item.Region, &item.StackName, &item.StackSlug, &item.StackURL, &item.OTLPEndpoint, &item.InstanceID, &item.Status, &progress, &item.Error, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(progress), &item.Progress); err != nil {
		return nil, err
	}
	if item.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if item.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) UpdateGrafanaStack(ctx context.Context, item domain.GrafanaStack) error {
	progress, err := json.Marshal(item.Progress)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE session_stacks SET stack_url = ?, otlp_endpoint = ?, instance_id = ?, status = ?, progress = ?, error = ?, updated_at = ? WHERE id = ? AND session_id = ?`, item.StackURL, item.OTLPEndpoint, item.InstanceID, item.Status, string(progress), item.Error, formatTime(time.Now().UTC()), item.ID, item.SessionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return ErrNotFound
	}
	return err
}
