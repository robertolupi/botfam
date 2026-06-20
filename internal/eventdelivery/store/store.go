package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Migration struct {
	Version int
	Name    string
	SQL     string
}

var Migrations = []Migration{
	{
		Version: 1,
		Name:    "event_delivery_v2_initial",
		SQL: `
CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  completed_at TEXT,
  status TEXT NOT NULL DEFAULT 'running'
);

CREATE TABLE raw_observations (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id),
  source TEXT NOT NULL,
  repo TEXT NOT NULL,
  artifact_kind TEXT NOT NULL,
  artifact_number INTEGER NOT NULL,
  notification_thread_id TEXT,
  event_kind TEXT NOT NULL,
  event_key TEXT NOT NULL,
  event_class TEXT NOT NULL CHECK (event_class IN ('stable-id', 'synthetic-id', 'query-only')),
  source_query TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  prior_state_json TEXT,
  observed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  processed_at TEXT,
  UNIQUE (source, repo, artifact_kind, artifact_number, event_kind, event_key)
);

CREATE TABLE work_items (
  id TEXT PRIMARY KEY,
  raw_observation_id TEXT REFERENCES raw_observations(id),
  kind TEXT NOT NULL,
  source_id TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  scope_generation INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (kind, source_id, scope_generation)
);

CREATE TABLE work_item_state_transitions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  from_state TEXT,
  to_state TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  transitioned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE dispatches (
  id TEXT PRIMARY KEY,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  worker_id TEXT NOT NULL,
  scope_generation INTEGER NOT NULL,
  dispatched_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  completed_at TEXT
);

CREATE TABLE forge_action_outbox (
  id TEXT PRIMARY KEY,
  work_item_id TEXT NOT NULL REFERENCES work_items(id),
  action_key TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  arguments_json TEXT NOT NULL,
  fencing_token INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  committed_at TEXT,
  UNIQUE (work_item_id, action_key)
);

CREATE TABLE action_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  outbox_id TEXT NOT NULL REFERENCES forge_action_outbox(id),
  fencing_token INTEGER NOT NULL,
  attempted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  result TEXT NOT NULL,
  response_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE git_action_log (
  id TEXT PRIMARY KEY,
  run_id TEXT REFERENCES runs(id),
  action TEXT NOT NULL,
  trace_id TEXT NOT NULL DEFAULT '',
  span_id TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE scope_generations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  repo TEXT NOT NULL,
  milestone_id INTEGER,
  scope_hash TEXT NOT NULL,
  source_query TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (repo, scope_hash, id)
);

CREATE TABLE scope_membership (
  scope_generation_id INTEGER NOT NULL REFERENCES scope_generations(id),
  artifact_kind TEXT NOT NULL,
  artifact_number INTEGER NOT NULL,
  disposition TEXT NOT NULL CHECK (disposition IN ('in_scope', 'needs_refresh', 'noise', 'leased_elsewhere')),
  reason TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (scope_generation_id, artifact_kind, artifact_number)
);

CREATE TABLE relation_predicates (
  id TEXT PRIMARY KEY,
  relation TEXT NOT NULL,
  source_query TEXT NOT NULL,
  stable_key_template TEXT NOT NULL,
  disposition TEXT NOT NULL CHECK (disposition IN ('in_scope', 'needs_refresh', 'noise')),
  configurable BOOLEAN NOT NULL DEFAULT 0
);

CREATE TABLE artifacts (
  id TEXT PRIMARY KEY,
  work_item_id TEXT REFERENCES work_items(id),
  kind TEXT NOT NULL,
  uri TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (kind, uri, sha256)
);

CREATE TABLE session_registry (
  session_id TEXT PRIMARY KEY,
  endpoint TEXT NOT NULL,
  lease_id TEXT NOT NULL,
  fencing_token INTEGER NOT NULL,
  scope_hash TEXT NOT NULL,
  scope_generation INTEGER NOT NULL,
  status TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
`,
	},
	{
		Version: 2,
		Name:    "seed_relation_predicates",
		SQL: `
INSERT INTO relation_predicates (id, relation, source_query, stable_key_template, disposition, configurable) VALUES
  ('milestone_membership', 'Milestone membership', 'GET /repos/{owner}/{repo}/issues?milestone={milestone_id}', 'artifact:{number}:milestone:{milestone_id}', 'needs_refresh', 0),
  ('explicit_dependency_edge', 'Explicit dependency edge', 'GET /repos/{owner}/{repo}/issues/{number}/dependencies', 'edge:{from}->{to}', 'needs_refresh', 0),
  ('pr_references_in_scope_issue', 'PR closes or references in-scope issue', 'GET /repos/{owner}/{repo}/pulls/{number}; parse body for closes/fixes/resolves #N', 'pull:{pull}:issue:{issue}', 'needs_refresh', 0),
  ('label_on_in_scope_artifact', 'Label on in-scope artifact', 'GET /repos/{owner}/{repo}/issues/{number}; inspect labels', 'artifact:{number}:label:{label_id}', 'noise', 1),
  ('assignment_change_on_in_scope_artifact', 'Assignment change on in-scope artifact', 'GET /repos/{owner}/{repo}/issues/{number}; inspect assignees', 'artifact:{number}:assignee:{user}', 'in_scope', 0),
  ('mention_out_of_scope_artifact', 'Mention of actor on out-of-scope artifact', 'GET /notifications?status-types=unread filtered by subject/mention', 'artifact:{number}:mention:{message_id}', 'noise', 0);
`,
	},
	{
		Version: 3,
		Name:    "event_delivery_v2_outbox_responses_and_telemetry",
		SQL: `
ALTER TABLE forge_action_outbox ADD COLUMN response_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE telemetry_spans (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  trace_id TEXT NOT NULL,
  span_id TEXT NOT NULL,
  component TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  timestamp TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
`,
	},
}

var observeStoreEvent = func(string) {}

type OutboxResult struct {
	ID           string
	Deduped      bool
	Committed    bool
	ResponseJSON string
}

type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	logGitSubprocess(ctx, name, args, err)
	return out, err
}

const (
	GitActionLogDBEnv = "BOTFAM_GIT_ACTION_LOG_DB"
	SeamTokenEnv      = "BOTFAM_SEAM_TOKEN"
)

type SessionRepoOptions struct {
	Dir               string
	RunNumber         int
	Runner            CommandRunner
	GitignorePatterns []string
}

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')));`); err != nil {
		return err
	}
	for _, migration := range Migrations {
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration.Version).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("migration %d %s: %w", migration.Version, migration.Name, err)
	}
	observeStoreEvent(fmt.Sprintf("migration %d", migration.Version))
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, migration.Version, migration.Name); err != nil {
		return err
	}
	return tx.Commit()
}

// StartRun inserts a row for a new supervisor run. raw_observations reference a
// run, so a run must exist before observations are ingested. It is idempotent on
// the run id (INSERT OR IGNORE) so re-entrant boots do not fail.
func StartRun(ctx context.Context, db *sql.DB, id, sessionID string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(sessionID) == "" {
		return errors.New("run id and session id are required")
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO runs (id, session_id) VALUES (?, ?)`, id, sessionID)
	return err
}

func EnqueueForgeAction(ctx context.Context, db *sql.DB, id, workItemID, actionKey, toolName, argumentsJSON string, fencingToken uint64) (OutboxResult, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(workItemID) == "" || strings.TrimSpace(actionKey) == "" {
		return OutboxResult{}, errors.New("id, work item id, and action key are required")
	}
	res, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO forge_action_outbox (id, work_item_id, action_key, tool_name, arguments_json, fencing_token)
VALUES (?, ?, ?, ?, ?, ?)`, id, workItemID, actionKey, toolName, argumentsJSON, fencingToken)
	if err != nil {
		return OutboxResult{}, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return OutboxResult{}, err
	}
	if rows == 1 {
		return OutboxResult{ID: id, ResponseJSON: "{}"}, nil
	}
	var existing OutboxResult
	var state string
	if err := db.QueryRowContext(ctx, `SELECT id, state, response_json FROM forge_action_outbox WHERE work_item_id = ? AND action_key = ?`, workItemID, actionKey).Scan(&existing.ID, &state, &existing.ResponseJSON); err != nil {
		return OutboxResult{}, err
	}
	existing.Deduped = true
	existing.Committed = state == "committed"
	return existing, nil
}

func RecordForgeActionAttempt(ctx context.Context, db *sql.DB, outboxID string, fencingToken uint64, result, responseJSON string) error {
	if strings.TrimSpace(outboxID) == "" {
		return errors.New("outbox id is required")
	}
	if strings.TrimSpace(result) == "" {
		return errors.New("attempt result is required")
	}
	if strings.TrimSpace(responseJSON) == "" {
		responseJSON = "{}"
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO action_attempts (outbox_id, fencing_token, result, response_json) VALUES (?, ?, ?, ?)`, outboxID, fencingToken, result, responseJSON); err != nil {
		return err
	}
	if result == "committed" {
		if _, err := tx.ExecContext(ctx, `UPDATE forge_action_outbox SET state = 'committed', committed_at = COALESCE(committed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), response_json = ? WHERE id = ?`, responseJSON, outboxID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func PendingWorkItems(ctx context.Context, db *sql.DB, limit int) ([]WorkItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `SELECT id, kind, source_id, title, body, scope_generation FROM work_items WHERE state = 'pending' ORDER BY created_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []WorkItem
	for rows.Next() {
		var item WorkItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.SourceID, &item.Title, &item.Body, &item.ScopeGeneration); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func RecordArtifact(ctx context.Context, db *sql.DB, id, workItemID, kind, uri, sha256 string) error {
	if strings.TrimSpace(id) == "" {
		id = uuid.NewString()
	}
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(kind) == "" || strings.TrimSpace(uri) == "" {
		return errors.New("work item id, kind, and uri are required")
	}
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO artifacts (id, work_item_id, kind, uri, sha256) VALUES (?, ?, ?, ?, ?)`, id, workItemID, kind, uri, sha256)
	return err
}

func RecordTelemetrySpan(ctx context.Context, db *sql.DB, traceID, spanID, component, eventType, payloadJSON, timestamp string) error {
	if strings.TrimSpace(payloadJSON) == "" {
		payloadJSON = "{}"
	}
	if strings.TrimSpace(timestamp) == "" {
		_, err := db.ExecContext(ctx, `INSERT INTO telemetry_spans (trace_id, span_id, component, event_type, payload_json) VALUES (?, ?, ?, ?, ?)`, traceID, spanID, component, eventType, payloadJSON)
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO telemetry_spans (trace_id, span_id, component, event_type, payload_json, timestamp) VALUES (?, ?, ?, ?, ?, ?)`, traceID, spanID, component, eventType, payloadJSON, timestamp)
	return err
}

func LogGitAction(ctx context.Context, db *sql.DB, id, runID, action, traceID, spanID, payloadJSON string) error {
	if strings.TrimSpace(id) == "" {
		id = uuid.NewString()
	}
	if strings.TrimSpace(action) == "" {
		return errors.New("git action is required")
	}
	if strings.TrimSpace(payloadJSON) == "" {
		payloadJSON = "{}"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO git_action_log (id, run_id, action, trace_id, span_id, payload_json) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?)`, id, runID, action, traceID, spanID, payloadJSON)
	return err
}

func logGitSubprocess(ctx context.Context, name string, args []string, runErr error) {
	if name != "git" {
		return
	}
	dbPath := os.Getenv(GitActionLogDBEnv)
	if dbPath == "" {
		return
	}
	db, err := Open(dbPath)
	if err != nil {
		return
	}
	defer db.Close()
	payload := map[string]any{
		"argv":       append([]string{name}, args...),
		"seam_token": os.Getenv(SeamTokenEnv),
	}
	if runErr != nil {
		payload["error"] = runErr.Error()
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = LogGitAction(ctx, db, "", "", strings.Join(append([]string{name}, args...), " "), "", "", string(payloadJSON))
}

type WorkItem struct {
	ID              string
	Kind            string
	SourceID        string
	Title           string
	Body            string
	ScopeGeneration uint64
}

func Dump(path string) ([]byte, error) {
	out, err := exec.Command("sqlite3", path, ".dump").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dump sqlite database %s: %w: %s", path, err, string(out))
	}
	return out, nil
}

func Restore(path string, dump []byte) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(string(dump))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restore sqlite dump: %w: %s", err, string(out))
	}
	return nil
}

func OpenSessionRepo(ctx context.Context, opts SessionRepoOptions) (*sql.DB, error) {
	if opts.Dir == "" {
		return nil, errors.New("session repo dir is required")
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	if err := os.MkdirAll(filepath.Join(opts.Dir, "artifacts"), 0o755); err != nil {
		return nil, fmt.Errorf("create artifacts dir: %w", err)
	}
	if err := EnsureSessionGitignore(opts.Dir, opts.GitignorePatterns...); err != nil {
		return nil, err
	}
	if err := CaptureCrashedRun(ctx, opts.Dir, opts.RunNumber, runner); err != nil {
		return nil, err
	}
	db, err := Open(filepath.Join(opts.Dir, "session.db"))
	if err != nil {
		return nil, err
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func CaptureCrashedRun(ctx context.Context, dir string, runNumber int, runner CommandRunner) error {
	if runner == nil {
		runner = ExecRunner{}
	}
	dbPath := filepath.Join(dir, "session.db")
	if _, err := os.Stat(dbPath); err == nil {
		if err := DumpToFile(dbPath, filepath.Join(dir, "session.sql")); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	out, err := runner.Run(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("inspect crashed-run dirty state: %w: %s", err, string(out))
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if out, err := runner.Run(ctx, dir, "git", "add", ".gitignore", "session.sql", "artifacts"); err != nil {
		return fmt.Errorf("stage crashed-run state: %w: %s", err, string(out))
	}
	message := fmt.Sprintf("crashed-run: run %d auto-captured state", runNumber)
	if out, err := runner.Run(ctx, dir, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("commit crashed-run state: %w: %s", err, string(out))
	}
	return nil
}

func DumpToFile(dbPath, dumpPath string) error {
	dump, err := Dump(dbPath)
	if err != nil {
		return err
	}
	tmp := dumpPath + ".tmp"
	if err := os.WriteFile(tmp, dump, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dumpPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	observeStoreEvent("dump session.sql")
	return fsyncDir(filepath.Dir(dumpPath))
}

func EnsureSessionGitignore(dir string, extraPatterns ...string) error {
	patterns := []string{
		"session.db",
		"session.db-wal",
		"session.db-shm",
	}
	for _, pattern := range extraPatterns {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	patterns = append(patterns, "")
	contents := strings.Join(patterns, "\n")
	path := filepath.Join(dir, ".gitignore")
	current, err := os.ReadFile(path)
	if err == nil && string(current) == contents {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return WriteCompleteFile(path, []byte(contents), 0o644)
}
