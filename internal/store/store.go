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

	"github.com/Alex3k/grafana-demo-compiler/internal/brieftopics"
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
  source_message_id TEXT NOT NULL DEFAULT '',
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
  progress TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(session_id, iteration_number)
);

CREATE INDEX IF NOT EXISTS prototype_iterations_session_number_idx
  ON prototype_iterations(session_id, iteration_number DESC);

CREATE TABLE IF NOT EXISTS deployments (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  prototype_iteration_id TEXT NOT NULL REFERENCES prototype_iterations(id) ON DELETE CASCADE,
  target TEXT NOT NULL CHECK (target = 'local'),
  region TEXT NOT NULL,
  stack_name TEXT NOT NULL,
  stack_slug TEXT NOT NULL,
  stack_url TEXT NOT NULL DEFAULT '',
  otlp_endpoint TEXT NOT NULL DEFAULT '',
  instance_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('provisioning', 'needs_token', 'starting', 'running', 'verifying', 'verified', 'failed', 'interrupted')),
  progress TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS deployments_session_created_idx
  ON deployments(session_id, created_at DESC);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
CREATE TABLE IF NOT EXISTS session_deletions (
  session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (3);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (5);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (7);
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
	var progressColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('prototype_iterations') WHERE name = 'progress'`).Scan(&progressColumnCount); err != nil {
		return fmt.Errorf("inspect prototype progress schema: %w", err)
	}
	if progressColumnCount == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE prototype_iterations ADD COLUMN progress TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("add prototype progress: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (6)`); err != nil {
		return fmt.Errorf("record prototype progress migration: %w", err)
	}
	var organizationColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('deployments') WHERE name = 'organization'`).Scan(&organizationColumnCount); err != nil {
		return fmt.Errorf("inspect deployment organization schema: %w", err)
	}
	if organizationColumnCount > 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE deployments DROP COLUMN organization`); err != nil {
			return fmt.Errorf("remove deployment organization: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (8)`); err != nil {
		return fmt.Errorf("record deployment organization migration: %w", err)
	}
	var briefCursorColumnCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('living_briefs') WHERE name = 'source_message_id'`).Scan(&briefCursorColumnCount); err != nil {
		return fmt.Errorf("inspect living brief cursor schema: %w", err)
	}
	if briefCursorColumnCount == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE living_briefs ADD COLUMN source_message_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add living brief cursor: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `
UPDATE living_briefs
SET source_message_id = COALESCE((
  SELECT messages.id
  FROM messages
  WHERE messages.session_id = living_briefs.session_id
    AND messages.kind = 'message'
    AND messages.status = 'complete'
    AND messages.role IN ('user', 'assistant')
    AND messages.created_at <= living_briefs.updated_at
  ORDER BY messages.created_at DESC, messages.id DESC
  LIMIT 1
), '')`); err != nil {
			return fmt.Errorf("backfill living brief cursor: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (9)`); err != nil {
		return fmt.Errorf("record living brief cursor migration: %w", err)
	}
	if err := s.migrateBriefTopicIDs(ctx); err != nil {
		return err
	}
	if err := s.migrateSessionStacks(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE messages SET status = 'interrupted' WHERE status = 'streaming';
UPDATE brief_thread_messages SET status = 'interrupted' WHERE status = 'streaming';
UPDATE operations SET status = 'interrupted', ended_at = ? WHERE status = 'running';
UPDATE prototype_iterations SET status = 'failed', error = 'Prototype generation was interrupted', updated_at = ? WHERE status = 'generating';
UPDATE deployments SET status = 'interrupted', error = 'Local deployment was interrupted by a server restart', updated_at = ? WHERE status IN ('provisioning', 'starting', 'verifying');
UPDATE sessions SET state = 'Generated' WHERE state IN ('Draft', 'Ready') AND EXISTS (
  SELECT 1 FROM prototype_iterations WHERE prototype_iterations.session_id = sessions.id AND prototype_iterations.status = 'complete'
);
`, formatTime(time.Now().UTC()), formatTime(time.Now().UTC()), formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("recover interrupted work: %w", err)
	}
	return nil
}

func (s *Store) migrateBriefTopicIDs(ctx context.Context) error {
	var alreadyApplied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 10`).Scan(&alreadyApplied); err != nil {
		return fmt.Errorf("inspect brief topic ID migration: %w", err)
	}
	if alreadyApplied > 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin brief topic ID migration: %w", err)
	}
	defer tx.Rollback()
	var columnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('brief_threads') WHERE name = 'topic_id'`).Scan(&columnCount); err != nil {
		return fmt.Errorf("inspect brief topic ID schema: %w", err)
	}
	if columnCount == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE brief_threads ADD COLUMN topic_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add brief topic ID: %w", err)
		}
	}

	type storedBrief struct {
		sessionID string
		version   int
		content   domain.BriefContent
	}
	rows, err := tx.QueryContext(ctx, `SELECT session_id, version, content FROM living_briefs ORDER BY session_id, version`)
	if err != nil {
		return fmt.Errorf("list briefs for topic ID migration: %w", err)
	}
	var briefs []storedBrief
	for rows.Next() {
		var item storedBrief
		var payload string
		if err := rows.Scan(&item.sessionID, &item.version, &payload); err != nil {
			rows.Close()
			return fmt.Errorf("scan brief for topic ID migration: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &item.content); err != nil {
			rows.Close()
			return fmt.Errorf("decode brief for topic ID migration: %w", err)
		}
		briefs = append(briefs, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	previous := map[string]*domain.BriefContent{}
	bySession := map[string][]domain.BriefContent{}
	for index := range briefs {
		brieftopics.Reconcile(previous[briefs[index].sessionID], &briefs[index].content)
		payload, err := json.Marshal(briefs[index].content)
		if err != nil {
			return fmt.Errorf("encode brief topic IDs: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE living_briefs SET content = ? WHERE session_id = ? AND version = ?`, string(payload), briefs[index].sessionID, briefs[index].version); err != nil {
			return fmt.Errorf("store brief topic IDs: %w", err)
		}
		copy := briefs[index].content
		previous[briefs[index].sessionID] = &copy
		bySession[briefs[index].sessionID] = append(bySession[briefs[index].sessionID], copy)
	}

	type storedThread struct{ id, sessionID, label, topicID string }
	rows, err = tx.QueryContext(ctx, `SELECT id, session_id, label, topic_id FROM brief_threads`)
	if err != nil {
		return fmt.Errorf("list threads for topic ID migration: %w", err)
	}
	var threads []storedThread
	for rows.Next() {
		var thread storedThread
		if err := rows.Scan(&thread.id, &thread.sessionID, &thread.label, &thread.topicID); err != nil {
			rows.Close()
			return fmt.Errorf("scan thread for topic ID migration: %w", err)
		}
		threads = append(threads, thread)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, thread := range threads {
		if thread.topicID != "" {
			continue
		}
		for index := len(bySession[thread.sessionID]) - 1; index >= 0; index-- {
			if focus, ok := brieftopics.ResolveLegacyLabel(&bySession[thread.sessionID][index], thread.label); ok {
				thread.topicID = focus.TopicID
				break
			}
		}
		if thread.topicID == "" {
			thread.topicID = "legacy_" + thread.id
		}
		if _, err := tx.ExecContext(ctx, `UPDATE brief_threads SET topic_id = ? WHERE id = ?`, thread.topicID, thread.id); err != nil {
			return fmt.Errorf("store brief thread topic ID: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS brief_threads_session_topic_idx ON brief_threads(session_id, topic_id, updated_at)`); err != nil {
		return fmt.Errorf("index brief thread topic IDs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (10)`); err != nil {
		return fmt.Errorf("record brief topic ID migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit brief topic ID migration: %w", err)
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
	deployments, err := s.ListDeployments(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	session.Deployments = deployments
	session.GrafanaStack, err = s.GetGrafanaStack(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Store) SessionDeleting(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_deletions WHERE session_id = ?`, id).Scan(&count)
	return count > 0, err
}

func (s *Store) BeginSessionDeletion(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO session_deletions(session_id)
 SELECT ? WHERE
 NOT EXISTS (SELECT 1 FROM operations WHERE session_id = ? AND status = 'running') AND
 NOT EXISTS (SELECT 1 FROM brief_thread_messages WHERE session_id = ? AND status = 'streaming') AND
 NOT EXISTS (SELECT 1 FROM prototype_iterations WHERE session_id = ? AND status = 'generating') AND
 NOT EXISTS (SELECT 1 FROM deployments WHERE session_id = ? AND status IN ('provisioning','starting','verifying')) AND
 NOT EXISTS (SELECT 1 FROM session_stacks WHERE session_id = ? AND status IN ('provisioning','awaiting_auth'))
 ON CONFLICT(session_id) DO UPDATE SET session_id = excluded.session_id`, id, id, id, id, id, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("wait for this session's active operations to finish before deleting")
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) CreateDeployment(ctx context.Context, deployment domain.Deployment) (domain.Deployment, error) {
	now := time.Now().UTC()
	if deployment.ID == "" {
		deployment.ID = newID()
	}
	deployment.CreatedAt, deployment.UpdatedAt = now, now
	if deployment.Progress == nil {
		deployment.Progress = []string{}
	}
	progress, err := json.Marshal(deployment.Progress)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("encode deployment progress: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO deployments(id, session_id, prototype_iteration_id, target, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, deployment.ID, deployment.SessionID, deployment.PrototypeIterationID, deployment.Target, deployment.Region, deployment.StackName, deployment.StackSlug, deployment.StackURL, deployment.OTLPEndpoint, deployment.InstanceID, deployment.Status, string(progress), formatTime(now), formatTime(now))
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("create deployment: %w", err)
	}
	return deployment, nil
}

func (s *Store) UpdateDeployment(ctx context.Context, deployment domain.Deployment) error {
	if deployment.Progress == nil {
		deployment.Progress = []string{}
	}
	progress, err := json.Marshal(deployment.Progress)
	if err != nil {
		return fmt.Errorf("encode deployment progress: %w", err)
	}
	deployment.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE deployments SET stack_url = ?, otlp_endpoint = ?, instance_id = ?, status = ?, progress = ?, error = ?, updated_at = ?
WHERE id = ? AND session_id = ?`, deployment.StackURL, deployment.OTLPEndpoint, deployment.InstanceID, deployment.Status, string(progress), deployment.Error, formatTime(deployment.UpdatedAt), deployment.ID, deployment.SessionID)
	if err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ClaimDeploymentStart(ctx context.Context, deployment domain.Deployment) (bool, error) {
	if deployment.Progress == nil {
		deployment.Progress = []string{}
	}
	progress, err := json.Marshal(deployment.Progress)
	if err != nil {
		return false, fmt.Errorf("encode deployment progress: %w", err)
	}
	deployment.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = ?, progress = ?, error = ?, updated_at = ?
WHERE id = ? AND session_id = ? AND status = 'needs_token'`, deployment.Status, string(progress), deployment.Error, formatTime(deployment.UpdatedAt), deployment.ID, deployment.SessionID)
	if err != nil {
		return false, fmt.Errorf("claim deployment start: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check deployment start claim: %w", err)
	}
	return count == 1, nil
}

func (s *Store) GetDeployment(ctx context.Context, sessionID, deploymentID string) (domain.Deployment, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, session_id, prototype_iteration_id, target, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, error, created_at, updated_at
FROM deployments WHERE id = ? AND session_id = ?`, deploymentID, sessionID)
	return scanDeployment(row)
}

func (s *Store) ListDeployments(ctx context.Context, sessionID string) ([]domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, prototype_iteration_id, target, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, error, created_at, updated_at
FROM deployments WHERE session_id = ? ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()
	result := []domain.Deployment{}
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, deployment)
	}
	return result, rows.Err()
}

func (s *Store) ListLocalCleanupCandidates(ctx context.Context) ([]domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, prototype_iteration_id, target, region, stack_name, stack_slug, stack_url, otlp_endpoint, instance_id, status, progress, error, created_at, updated_at
FROM deployments
WHERE target = 'local'
  AND (status IN ('running', 'verifying', 'verified', 'failed') OR (status = 'interrupted' AND error <> ''))
ORDER BY session_id, created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list active local deployments: %w", err)
	}
	defer rows.Close()
	result := []domain.Deployment{}
	for rows.Next() {
		deployment, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, deployment)
	}
	return result, rows.Err()
}

func (s *Store) SetSessionState(ctx context.Context, sessionID, state string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET state = ?, updated_at = ? WHERE id = ?`, state, formatTime(time.Now().UTC()), sessionID)
	if err != nil {
		return fmt.Errorf("set session state: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
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
		Status: "generating", Artifacts: []domain.PrototypeArtifact{}, Checks: []domain.PrototypeCheck{}, Progress: []string{"Starting a new local prototype iteration"},
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO prototype_iterations(id, session_id, iteration_number, brief_version, status, progress, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, iteration.ID, sessionID, number, briefVersion, iteration.Status, `["Starting a new local prototype iteration"]`, formatTime(now), formatTime(now))
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

func (s *Store) UpdatePrototypeProgress(ctx context.Context, iteration domain.PrototypeIteration) error {
	if iteration.Progress == nil {
		iteration.Progress = []string{}
	}
	progress, err := json.Marshal(iteration.Progress)
	if err != nil {
		return fmt.Errorf("encode prototype progress: %w", err)
	}
	iteration.UpdatedAt = time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
UPDATE prototype_iterations SET root_path = ?, progress = ?, updated_at = ? WHERE id = ? AND session_id = ?`,
		iteration.RootPath, string(progress), formatTime(iteration.UpdatedAt), iteration.ID, iteration.SessionID)
	if err != nil {
		return fmt.Errorf("update prototype progress: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FinishPrototypeIteration(ctx context.Context, iteration domain.PrototypeIteration) error {
	if iteration.Artifacts == nil {
		iteration.Artifacts = []domain.PrototypeArtifact{}
	}
	if iteration.Checks == nil {
		iteration.Checks = []domain.PrototypeCheck{}
	}
	if iteration.Progress == nil {
		iteration.Progress = []string{}
	}
	artifacts, err := json.Marshal(iteration.Artifacts)
	if err != nil {
		return fmt.Errorf("encode prototype artifacts: %w", err)
	}
	checks, err := json.Marshal(iteration.Checks)
	if err != nil {
		return fmt.Errorf("encode prototype checks: %w", err)
	}
	progress, err := json.Marshal(iteration.Progress)
	if err != nil {
		return fmt.Errorf("encode prototype progress: %w", err)
	}
	iteration.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin prototype completion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE prototype_iterations
SET status = ?, root_path = ?, summary = ?, artifacts = ?, checks = ?, progress = ?, error = ?, updated_at = ?
WHERE id = ? AND session_id = ?`, iteration.Status, iteration.RootPath, iteration.Summary, string(artifacts), string(checks), string(progress), iteration.Error, formatTime(iteration.UpdatedAt), iteration.ID, iteration.SessionID)
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
SELECT id, session_id, iteration_number, brief_version, status, root_path, summary, artifacts, checks, progress, error, created_at, updated_at
FROM prototype_iterations WHERE session_id = ? ORDER BY iteration_number DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prototype iterations: %w", err)
	}
	defer rows.Close()
	iterations := make([]domain.PrototypeIteration, 0)
	for rows.Next() {
		var iteration domain.PrototypeIteration
		var artifacts, checks, progress, created, updated string
		if err := rows.Scan(&iteration.ID, &iteration.SessionID, &iteration.Number, &iteration.BriefVersion, &iteration.Status, &iteration.RootPath, &iteration.Summary, &artifacts, &checks, &progress, &iteration.Error, &created, &updated); err != nil {
			return nil, fmt.Errorf("scan prototype iteration: %w", err)
		}
		if err := json.Unmarshal([]byte(artifacts), &iteration.Artifacts); err != nil {
			return nil, fmt.Errorf("decode prototype artifacts: %w", err)
		}
		if err := json.Unmarshal([]byte(checks), &iteration.Checks); err != nil {
			return nil, fmt.Errorf("decode prototype checks: %w", err)
		}
		if err := json.Unmarshal([]byte(progress), &iteration.Progress); err != nil {
			return nil, fmt.Errorf("decode prototype progress: %w", err)
		}
		if iteration.Artifacts == nil {
			iteration.Artifacts = []domain.PrototypeArtifact{}
		}
		if iteration.Checks == nil {
			iteration.Checks = []domain.PrototypeCheck{}
		}
		if iteration.Progress == nil {
			iteration.Progress = []string{}
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
	return s.SaveBriefWithCursor(ctx, sessionID, "", content)
}

func (s *Store) SaveBriefWithCursor(ctx context.Context, sessionID, sourceMessageID string, content domain.BriefContent) (domain.LivingBrief, error) {
	brieftopics.Ensure(&content)
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO living_briefs(session_id, version, source_message_id, content, updated_at) VALUES (?, ?, ?, ?, ?)`, sessionID, version, sourceMessageID, string(payload), formatTime(now)); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("save living brief: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("touch session for living brief: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("commit living brief: %w", err)
	}
	return domain.LivingBrief{Version: version, SourceMessageID: sourceMessageID, UpdatedAt: now, Content: content}, nil
}

// AdvanceBriefCursor records processed conversation without creating a revision.
// A concurrent revision must not have its cursor overwritten by this snapshot.
func (s *Store) AdvanceBriefCursor(ctx context.Context, sessionID string, version int, sourceMessageID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE living_briefs SET source_message_id = ? WHERE session_id = ? AND version = ? AND version = (SELECT MAX(version) FROM living_briefs WHERE session_id = ?)`, sourceMessageID, sessionID, version, sessionID)
	return err
}

func (s *Store) GetBrief(ctx context.Context, sessionID string) (domain.LivingBrief, error) {
	var brief domain.LivingBrief
	var payload, updated string
	err := s.db.QueryRowContext(ctx, `SELECT version, source_message_id, content, updated_at FROM living_briefs WHERE session_id = ? ORDER BY version DESC LIMIT 1`, sessionID).Scan(&brief.Version, &brief.SourceMessageID, &payload, &updated)
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
	return s.ListMessagesSince(ctx, sessionID, "")
}

func (s *Store) ListMessagesSince(ctx context.Context, sessionID, afterMessageID string) ([]domain.Message, error) {
	query := `
SELECT id, session_id, role, kind, content, status, created_at
FROM messages WHERE session_id = ?`
	args := []any{sessionID}
	if afterMessageID != "" {
		query += ` AND (created_at, id) > (
  SELECT created_at, id FROM messages WHERE session_id = ? AND id = ?
)`
		args = append(args, sessionID, afterMessageID)
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, query, args...)
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
SELECT id, session_id, topic_id, label, value, status, candidate_value, state, created_at, updated_at
FROM brief_threads
WHERE session_id = ? AND topic_id = ? AND state = 'draft'
ORDER BY updated_at DESC LIMIT 1
`, sessionID, focus.TopicID)
	thread, err := scanBriefThread(row)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.BriefThread{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		thread = domain.BriefThread{ID: newID(), SessionID: sessionID, Focus: focus, State: "draft", CreatedAt: now, UpdatedAt: now}
		_, err = s.db.ExecContext(ctx, `
INSERT INTO brief_threads(id, session_id, topic_id, label, value, status, candidate_value, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, thread.ID, thread.SessionID, thread.Focus.TopicID, thread.Focus.Label, thread.Focus.Value, thread.Focus.Status, thread.CandidateValue, thread.State, formatTime(now), formatTime(now))
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
SELECT id, session_id, topic_id, label, value, status, candidate_value, state, created_at, updated_at
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
	brieftopics.Ensure(&content)
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
	var sourceMessageID string
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version), 0) + 1,
       COALESCE((SELECT source_message_id FROM living_briefs WHERE session_id = ? ORDER BY version DESC LIMIT 1), '')
FROM living_briefs WHERE session_id = ?
`, sessionID, sessionID).Scan(&version, &sourceMessageID); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("find confirmed brief version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO living_briefs(session_id, version, source_message_id, content, updated_at) VALUES (?, ?, ?, ?, ?)`, sessionID, version, sourceMessageID, string(payload), formatTime(now)); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("save confirmed brief: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, formatTime(now), sessionID); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("touch session for confirmed brief: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.LivingBrief{}, fmt.Errorf("commit confirmed brief: %w", err)
	}
	return domain.LivingBrief{Version: version, SourceMessageID: sourceMessageID, UpdatedAt: now, Content: content}, nil
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

func scanDeployment(row scanner) (domain.Deployment, error) {
	var deployment domain.Deployment
	var progress, created, updated string
	if err := row.Scan(
		&deployment.ID, &deployment.SessionID, &deployment.PrototypeIterationID,
		&deployment.Target, &deployment.Region,
		&deployment.StackName, &deployment.StackSlug, &deployment.StackURL,
		&deployment.OTLPEndpoint, &deployment.InstanceID, &deployment.Status, &progress, &deployment.Error,
		&created, &updated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Deployment{}, ErrNotFound
		}
		return domain.Deployment{}, fmt.Errorf("scan deployment: %w", err)
	}
	if err := json.Unmarshal([]byte(progress), &deployment.Progress); err != nil {
		return domain.Deployment{}, fmt.Errorf("decode deployment progress: %w", err)
	}
	if deployment.Progress == nil {
		deployment.Progress = []string{}
	}
	var err error
	deployment.CreatedAt, err = parseTime(created)
	if err != nil {
		return domain.Deployment{}, err
	}
	deployment.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return domain.Deployment{}, err
	}
	return deployment, nil
}

func scanBriefThread(row scanner) (domain.BriefThread, error) {
	var thread domain.BriefThread
	var created, updated string
	if err := row.Scan(&thread.ID, &thread.SessionID, &thread.Focus.TopicID, &thread.Focus.Label, &thread.Focus.Value, &thread.Focus.Status, &thread.CandidateValue, &thread.State, &created, &updated); err != nil {
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
