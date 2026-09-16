package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Alex3k/grafana-demo-compiler/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('Draft', 'Ready', 'Generated', 'Running', 'Verified')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
  kind TEXT NOT NULL CHECK (kind IN ('message', 'activity')),
  content TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('streaming', 'complete', 'failed', 'interrupted')),
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_session_created_idx
  ON messages(session_id, created_at, id);

CREATE TABLE IF NOT EXISTS operations (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'complete', 'failed', 'interrupted')),
  summary TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  ended_at TEXT
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE messages SET status = 'interrupted' WHERE status = 'streaming';
UPDATE operations SET status = 'interrupted', ended_at = ? WHERE status = 'running';
`, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("recover interrupted work: %w", err)
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, title string) (domain.Session, error) {
	if title == "" {
		title = "Untitled demo"
	}
	now := time.Now().UTC()
	session := domain.Session{ID: newID(), Title: title, State: "Draft", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(id, title, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		session.ID, session.Title, session.State, formatTime(now), formatTime(now),
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, state, created_at, updated_at FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]domain.Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, id string) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, state, created_at, updated_at FROM sessions WHERE id = ?`, id)
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	messages, err := s.ListMessages(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	session.Messages = messages
	return session, nil
}

func (s *Store) RenameSession(ctx context.Context, id, title string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`, title, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("rename session: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateMessage(ctx context.Context, message domain.Message) (domain.Message, error) {
	if message.ID == "" {
		message.ID = newID()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO messages(id, session_id, role, kind, content, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`, message.ID, message.SessionID, message.Role, message.Kind, message.Content, message.Status, formatTime(message.CreatedAt))
	if err != nil {
		return domain.Message{}, fmt.Errorf("create message: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), message.SessionID)
	if err != nil {
		return domain.Message{}, fmt.Errorf("touch session: %w", err)
	}
	return message, nil
}

func (s *Store) UpdateMessage(ctx context.Context, id, content, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET content = ?, status = ? WHERE id = ?`, content, status, id)
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	return nil
}

func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, role, kind, content, status, created_at
FROM messages WHERE session_id = ? ORDER BY created_at, id
`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]domain.Message, 0)
	for rows.Next() {
		var message domain.Message
		var created string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Kind, &message.Content, &message.Status, &created); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		message.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) CreateOperation(ctx context.Context, operation domain.Operation) (domain.Operation, error) {
	if operation.ID == "" {
		operation.ID = newID()
	}
	if operation.StartedAt.IsZero() {
		operation.StartedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO operations(id, session_id, kind, status, summary, error, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
`, operation.ID, operation.SessionID, operation.Kind, operation.Status, operation.Summary, operation.Error, formatTime(operation.StartedAt))
	if err != nil {
		return domain.Operation{}, fmt.Errorf("create operation: %w", err)
	}
	return operation, nil
}

func (s *Store) FinishOperation(ctx context.Context, id, status, summary, errorText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE operations SET status = ?, summary = ?, error = ?, ended_at = ? WHERE id = ?`,
		status, summary, errorText, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("finish operation: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (domain.Session, error) {
	var session domain.Session
	var created, updated string
	if err := row.Scan(&session.ID, &session.Title, &session.State, &created, &updated); err != nil {
		return domain.Session{}, err
	}
	var err error
	session.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Session{}, err
	}
	session.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return parsed, nil
}
