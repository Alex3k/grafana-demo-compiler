package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
)

func (s *Store) initRevisions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS revision_proposals(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,payload TEXT NOT NULL,status TEXT NOT NULL,iteration_id TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '')`)
	return err
}
func (s *Store) CreateRevision(ctx context.Context, r domain.RevisionProposal) (domain.RevisionProposal, error) {
	if err := s.initRevisions(ctx); err != nil {
		return r, err
	}
	r.ID = newID()
	r.Status = "pending"
	payload, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO revision_proposals(id,session_id,payload,status) VALUES(?,?,?,?)`, r.ID, r.SessionID, string(payload), r.Status)
	return r, err
}
func (s *Store) ListRevisions(ctx context.Context, id string) ([]domain.RevisionProposal, error) {
	if err := s.initRevisions(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.payload,COALESCE(p.status,r.status),r.iteration_id,COALESCE(NULLIF(p.error,''),r.error) FROM revision_proposals r LEFT JOIN prototype_iterations p ON p.id=r.iteration_id WHERE r.session_id=? ORDER BY r.rowid DESC LIMIT 30`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.RevisionProposal{}
	for rows.Next() {
		var payload, status, iterationID, problem string
		if err := rows.Scan(&payload, &status, &iterationID, &problem); err != nil {
			return nil, err
		}
		var r domain.RevisionProposal
		if err := json.Unmarshal([]byte(payload), &r); err != nil {
			return nil, err
		}
		r.Status = status
		r.IterationID = iterationID
		r.Error = problem
		result = append(result, r)
	}
	return result, rows.Err()
}
func (s *Store) ClaimRevision(ctx context.Context, sessionID, id, status string) (domain.RevisionProposal, error) {
	var payload string
	var r domain.RevisionProposal
	if status != "approved" && status != "rejected" {
		return r, errors.New("invalid decision")
	}
	err := s.db.QueryRowContext(ctx, `UPDATE revision_proposals SET status=? WHERE id=? AND session_id=? AND status='pending' RETURNING payload`, status, id, sessionID).Scan(&payload)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal([]byte(payload), &r)
	r.Status = status
	return r, err
}
func (s *Store) FinishRevisionApproval(ctx context.Context, id, iterationID, problem string) error {
	status := "building"
	if problem != "" {
		status = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE revision_proposals SET status=?,iteration_id=?,error=? WHERE id=?`, status, iterationID, problem, id)
	return err
}
