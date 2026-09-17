package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

CREATE TABLE IF NOT EXISTS living_briefs (
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  content TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (session_id, version)
);

CREATE TABLE IF NOT EXISTS brief_threads (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  value TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('proposed', 'confirmed')),
  candidate_value TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL CHECK (state IN ('draft', 'confirmed')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS brief_threads_session_label_idx
  ON brief_threads(session_id, label, updated_at);

CREATE TABLE IF NOT EXISTS brief_thread_messages (
  id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL REFERENCES brief_threads(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
  kind TEXT NOT NULL CHECK (kind IN ('message', 'activity')),
  content TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('streaming', 'complete', 'failed', 'interrupted')),
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS brief_thread_messages_thread_created_idx
  ON brief_thread_messages(thread_id, created_at, id);

CREATE TABLE IF NOT EXISTS prototype_iterations (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  iteration_number INTEGER NOT NULL,
  brief_version INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('generating', 'complete', 'failed')),
  root_path TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  artifacts TEXT NOT NULL DEFAULT '[]',
  checks TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(session_id, iteration_number)
);

CREATE INDEX IF NOT EXISTS prototype_iterations_session_number_idx
  ON prototype_iterations(session_id, iteration_number DESC);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (3);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (5);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	var candidateColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('brief_threads') WHERE name = 'candidate_value'`).Scan(&candidateColumnCount); err != nil {
		return fmt.Errorf("inspect brief thread schema: %w", err)
	}
	if candidateColumnCount == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE brief_threads ADD COLUMN candidate_value TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add brief thread candidate: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (4)`); err != nil {
		return fmt.Errorf("record brief thread candidate migration: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE messages SET status = 'interrupted' WHERE status = 'streaming';
UPDATE brief_thread_messages SET status = 'interrupted' WHERE status = 'streaming';
UPDATE operations SET status = 'interrupted', ended_at = ? WHERE status = 'running';
UPDATE prototype_iterations SET status = 'failed', error = 'Prototype generation was interrupted', updated_at = ? WHERE status = 'generating';
`, formatTime(time.Now().UTC()), formatTime(time.Now().UTC()))
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
	brief, err := s.GetBrief(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Session{}, err
	}
	if err == nil {
		session.Brief = &brief
	}
	prototypes, err := s.ListPrototypeIterations(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	session.Prototypes = prototypes
	return session, nil
}

func (s *Store) CreatePrototypeIteration(ctx context.Context, sessionID string, briefVersion int) (domain.PrototypeIteration, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PrototypeIteration{}, fmt.Errorf("begin prototype iteration: %w", err)
	}
	defer tx.Rollback()
	var number int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(iteration_number), 0) + 1 FROM prototype_iterations WHERE session_id = ?`, sessionID).Scan(&number); err != nil {
		return domain.PrototypeIteration{}, fmt.Errorf("find prototype iteration number: %w", err)
	}
	iteration := domain.PrototypeIteration{
		ID: newID(), SessionID: sessionID, Number: number, BriefVersion: briefVersion,
		Status: "generating", Artifacts: []domain.PrototypeArtifact{}, Checks: []domain.PrototypeCheck{},
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO prototype_iterations(id, session_id, iteration_number, brief_version, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, iteration.ID, sessionID, number, briefVersion, iteration.Status, formatTime(now), formatTime(now))
	if err != nil {
		return domain.PrototypeIteration{}, fmt.Errorf("create prototype iteration: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID); err != nil {
		return domain.PrototypeIteration{}, fmt.Errorf("touch session for prototype: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.PrototypeIteration{}, fmt.Errorf("commit prototype iteration: %w", err)
	}
	return iteration, nil
}

func (s *Store) FinishPrototypeIteration(ctx context.Context, iteration domain.PrototypeIteration) error {
	if iteration.Artifacts == nil {
		iteration.Artifacts = []domain.PrototypeArtifact{}
	}
	if iteration.Checks == nil {
		iteration.Checks = []domain.PrototypeCheck{}
	}
	artifacts, err := json.Marshal(iteration.Artifacts)
	if err != nil {
		return fmt.Errorf("encode prototype artifacts: %w", err)
	}
	checks, err := json.Marshal(iteration.Checks)
	if err != nil {
		return fmt.Errorf("encode prototype checks: %w", err)
	}
	iteration.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin prototype completion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE prototype_iterations
SET status = ?, root_path = ?, summary = ?, artifacts = ?, checks = ?, error = ?, updated_at = ?
WHERE id = ? AND session_id = ?`, iteration.Status, iteration.RootPath, iteration.Summary, string(artifacts), string(checks), iteration.Error, formatTime(iteration.UpdatedAt), iteration.ID, iteration.SessionID)
	if err != nil {
		return fmt.Errorf("finish prototype iteration: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(iteration.UpdatedAt), iteration.SessionID); err != nil {
		return fmt.Errorf("touch session after prototype: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit prototype completion: %w", err)
	}
	return nil
}

func (s *Store) ListPrototypeIterations(ctx context.Context, sessionID string) ([]domain.PrototypeIteration, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, iteration_number, brief_version, status, root_path, summary, artifacts, checks, error, created_at, updated_at
FROM prototype_iterations WHERE session_id = ? ORDER BY iteration_number DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prototype iterations: %w", err)
	}
	defer rows.Close()
	iterations := make([]domain.PrototypeIteration, 0)
	for rows.Next() {
		var iteration domain.PrototypeIteration
		var artifacts, checks, created, updated string
		if err := rows.Scan(&iteration.ID, &iteration.SessionID, &iteration.Number, &iteration.BriefVersion, &iteration.Status, &iteration.RootPath, &iteration.Summary, &artifacts, &checks, &iteration.Error, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan prototype iteration: %w", err)
		}
		if err := json.Unmarshal([]byte(artifacts), &iteration.Artifacts); err != nil {
			return nil, fmt.Errorf("decode prototype artifacts: %w", err)
		}
		if err := json.Unmarshal([]byte(checks), &iteration.Checks); err != nil {
			return nil, fmt.Errorf("decode prototype checks: %w", err)
		}
		if iteration.Artifacts == nil {
			iteration.Artifacts = []domain.PrototypeArtifact{}
		}
		if iteration.Checks == nil {
			iteration.Checks = []domain.PrototypeCheck{}
		}
		iteration.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		iteration.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, err
		}
		iterations = append(iterations, iteration)
	}
	return iterations, rows.Err()
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

func (s *Store) UpdateSessionState(ctx context.Context, id, state string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`, state, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveBrief(ctx context.Context, sessionID string, content domain.BriefContent) (domain.LivingBrief, error) {
	payload, err := json.Marshal(content)
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("encode living brief: %w", err)
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("begin living brief update: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM living_briefs WHERE session_id = ?`, sessionID).Scan(&version); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("find living brief version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO living_briefs(session_id, version, content, updated_at) VALUES (?, ?, ?, ?)`, sessionID, version, string(payload), formatTime(now)); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("save living brief: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("touch session for living brief: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("commit living brief: %w", err)
	}
	return domain.LivingBrief{Version: version, UpdatedAt: now, Content: content}, nil
}

func (s *Store) GetBrief(ctx context.Context, sessionID string) (domain.LivingBrief, error) {
	var brief domain.LivingBrief
	var payload, updated string
	err := s.db.QueryRowContext(ctx, `SELECT version, content, updated_at FROM living_briefs WHERE session_id = ? ORDER BY version DESC LIMIT 1`, sessionID).Scan(&brief.Version, &payload, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.LivingBrief{}, ErrNotFound
	}
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("get living brief: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &brief.Content); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("decode living brief: %w", err)
	}
	brief.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.LivingBrief{}, err
	}
	return brief, nil
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

func (s *Store) OpenBriefThread(ctx context.Context, sessionID string, focus domain.BriefFocus) (domain.BriefThread, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, session_id, label, value, status, candidate_value, state, created_at, updated_at
FROM brief_threads
WHERE session_id = ? AND label = ? AND state = 'draft'
ORDER BY updated_at DESC LIMIT 1
`, sessionID, focus.Label)
	thread, err := scanBriefThread(row)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.BriefThread{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		thread = domain.BriefThread{ID: newID(), SessionID: sessionID, Focus: focus, State: "draft", CreatedAt: now, UpdatedAt: now}
		_, err = s.db.ExecContext(ctx, `
INSERT INTO brief_threads(id, session_id, label, value, status, candidate_value, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
`, thread.ID, thread.SessionID, thread.Focus.Label, thread.Focus.Value, thread.Focus.Status, thread.CandidateValue, thread.State, formatTime(now), formatTime(now))
		if err != nil {
			return domain.BriefThread{}, fmt.Errorf("create brief thread: %w", err)
		}
	}
	thread.Messages, err = s.ListBriefThreadMessages(ctx, thread.ID)
	if err != nil {
		return domain.BriefThread{}, err
	}
	return thread, nil
}

func (s *Store) GetBriefThread(ctx context.Context, sessionID, threadID string) (domain.BriefThread, error) {
	thread, err := scanBriefThread(s.db.QueryRowContext(ctx, `
SELECT id, session_id, label, value, status, candidate_value, state, created_at, updated_at
FROM brief_threads WHERE id = ? AND session_id = ?
`, threadID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BriefThread{}, ErrNotFound
	}
	if err != nil {
		return domain.BriefThread{}, err
	}
	thread.Messages, err = s.ListBriefThreadMessages(ctx, thread.ID)
	if err != nil {
		return domain.BriefThread{}, err
	}
	return thread, nil
}

func (s *Store) CreateBriefThreadMessage(ctx context.Context, threadID string, message domain.Message) (domain.Message, error) {
	if message.ID == "" {
		message.ID = newID()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	now := formatTime(message.CreatedAt)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO brief_thread_messages(id, thread_id, session_id, role, kind, content, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, message.ID, threadID, message.SessionID, message.Role, message.Kind, message.Content, message.Status, now)
	if err != nil {
		return domain.Message{}, fmt.Errorf("create brief thread message: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE brief_threads SET updated_at = ? WHERE id = ?`, now, threadID)
	if err != nil {
		return domain.Message{}, fmt.Errorf("touch brief thread: %w", err)
	}
	return message, nil
}

func (s *Store) UpdateBriefThreadMessage(ctx context.Context, id, content, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE brief_thread_messages SET content = ?, status = ? WHERE id = ?`, content, status, id)
	if err != nil {
		return fmt.Errorf("update brief thread message: %w", err)
	}
	return nil
}

func (s *Store) UpdateBriefThreadCandidate(ctx context.Context, sessionID, threadID, value string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE brief_threads SET candidate_value = ?, updated_at = ? WHERE id = ? AND session_id = ? AND state = 'draft'`, value, formatTime(time.Now().UTC()), threadID, sessionID)
	if err != nil {
		return fmt.Errorf("update brief thread candidate: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListBriefThreadMessages(ctx context.Context, threadID string) ([]domain.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, role, kind, content, status, created_at
FROM brief_thread_messages WHERE thread_id = ? ORDER BY created_at, id
`, threadID)
	if err != nil {
		return nil, fmt.Errorf("list brief thread messages: %w", err)
	}
	defer rows.Close()
	messages := make([]domain.Message, 0)
	for rows.Next() {
		var message domain.Message
		var created string
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Kind, &message.Content, &message.Status, &created); err != nil {
			return nil, fmt.Errorf("scan brief thread message: %w", err)
		}
		message.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) ApplyBriefThread(ctx context.Context, sessionID, threadID string, content domain.BriefContent) (domain.LivingBrief, error) {
	payload, err := json.Marshal(content)
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("encode confirmed brief: %w", err)
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("begin confirmed brief update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE brief_threads SET state = 'confirmed', updated_at = ? WHERE id = ? AND session_id = ? AND state = 'draft' AND candidate_value <> ''`, formatTime(now), threadID, sessionID)
	if err != nil {
		return domain.LivingBrief{}, fmt.Errorf("confirm brief thread: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.LivingBrief{}, ErrNotFound
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM living_briefs WHERE session_id = ?`, sessionID).Scan(&version); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("find confirmed brief version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO living_briefs(session_id, version, content, updated_at) VALUES (?, ?, ?, ?)`, sessionID, version, string(payload), formatTime(now)); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("save confirmed brief: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("touch session for confirmed brief: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("commit confirmed brief: %w", err)
	}
	return domain.LivingBrief{Version: version, UpdatedAt: now, Content: content}, nil
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

func scanBriefThread(row scanner) (domain.BriefThread, error) {
	var thread domain.BriefThread
	var created, updated string
	if err := row.Scan(&thread.ID, &thread.SessionID, &thread.Focus.Label, &thread.Focus.Value, &thread.Focus.Status, &thread.CandidateValue, &thread.State, &created, &updated); err != nil {
		return domain.BriefThread{}, err
	}
	var err error
	thread.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.BriefThread{}, err
	}
	thread.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.BriefThread{}, err
	}
	return thread, nil
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
