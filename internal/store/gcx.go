package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Alex3k/grafana-demo-compiler/internal/gcxtool"
)

func (s *Store) initGCX(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS gcx_actions (
	 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	 stack TEXT NOT NULL, request TEXT NOT NULL, status TEXT NOT NULL, output TEXT NOT NULL DEFAULT '',
	 created_at INTEGER NOT NULL DEFAULT (unixepoch()))`)
	return err
}

func (s *Store) CreateGCXAction(ctx context.Context, a gcxtool.Action) (gcxtool.Action, error) {
	if err := s.initGCX(ctx); err != nil {
		return a, err
	}
	a.ID = newID()
	payload, err := json.Marshal(a.Request)
	if err != nil {
		return a, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gcx_actions(id,session_id,stack,request,status,output) VALUES(?,?,?,?,?,?)`, a.ID, a.SessionID, a.Stack, string(payload), a.Status, a.Output)
	return a, err
}

func (s *Store) ListGCXActions(ctx context.Context, sessionID string) ([]gcxtool.Action, error) {
	if err := s.initGCX(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,stack,request,status,output FROM gcx_actions WHERE session_id=? ORDER BY created_at DESC,rowid DESC LIMIT 30`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []gcxtool.Action{}
	for rows.Next() {
		var a gcxtool.Action
		var payload string
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Stack, &payload, &a.Status, &a.Output); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &a.Request); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// Claim atomically consumes a pending approval. Running/failed actions cannot be
// replayed: a new proposal and approval are required, including after a restart.
func (s *Store) ClaimGCXAction(ctx context.Context, sessionID, id, status string) (gcxtool.Action, error) {
	var a gcxtool.Action
	var payload string
	err := s.db.QueryRowContext(ctx, `UPDATE gcx_actions SET status=? WHERE session_id=? AND id=? AND status='pending' RETURNING id,session_id,stack,request,status,output`, status, sessionID, id).Scan(&a.ID, &a.SessionID, &a.Stack, &payload, &a.Status, &a.Output)
	if err != nil {
		return a, fmt.Errorf("approval no longer pending: %w", err)
	}
	err = json.Unmarshal([]byte(payload), &a.Request)
	return a, err
}

func (s *Store) FinishGCXAction(ctx context.Context, id, status, output string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE gcx_actions SET status=?,output=? WHERE id=?`, status, output, id)
	return err
}
