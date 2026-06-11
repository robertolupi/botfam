package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type SqliteStore struct {
	Root string
	db   *sql.DB
	mu   sync.Mutex
}

func (s *SqliteStore) RootPath() string {
	return s.Root
}

func (s *SqliteStore) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initUnlocked()
}

func (s *SqliteStore) initUnlocked() error {
	if s.db != nil {
		return nil
	}

	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}

	for _, dir := range []string{"tmp", "tasks/open", "tasks/claimed", "tasks/done"} {
		if err := os.MkdirAll(filepath.Join(s.Root, dir), 0o755); err != nil {
			return err
		}
	}

	dbPath := filepath.Join(s.Root, "botfam.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to set busy timeout: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	s.db = db

	if err := s.runMigrations(); err != nil {
		_ = db.Close()
		s.db = nil
		return err
	}

	return nil
}

func (s *SqliteStore) runMigrations() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at REAL NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	migrations := []string{
		// Migration 1: Initial schema
		`
		CREATE TABLE IF NOT EXISTS sessions (
			slug TEXT PRIMARY KEY,
			participants TEXT,
			created_by TEXT,
			created_at REAL,
			decision_rule TEXT,
			goals TEXT,
			guardrails TEXT,
			archived INTEGER DEFAULT 0,
			closed_at REAL,
			outcome TEXT
		);

		CREATE TABLE IF NOT EXISTS session_entries (
			id TEXT PRIMARY KEY,
			session_slug TEXT,
			actor TEXT,
			ts REAL,
			body TEXT,
			handoff_task TEXT,
			handoff_context TEXT,
			handoff_deliverable TEXT,
			FOREIGN KEY(session_slug) REFERENCES sessions(slug)
		);

		CREATE TABLE IF NOT EXISTS votes (
			proposal_id TEXT,
			actor TEXT,
			verdict TEXT,
			commit_sha TEXT,
			ts REAL,
			uds_connected INTEGER DEFAULT 0,
			PRIMARY KEY (proposal_id, actor)
		);

		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			type TEXT,
			payload TEXT,
			status TEXT,
			owner TEXT,
			created_at REAL,
			claimed_at REAL,
			lease_expires_at REAL,
			result TEXT,
			completed_at REAL,
			abandoned_at REAL,
			abandoned_by TEXT,
			abandoned_reason TEXT,
			swept_at REAL,
			swept_from TEXT,
			filename TEXT
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			from_actor TEXT,
			to_actor TEXT,
			type TEXT,
			payload TEXT,
			ts REAL,
			in_reply_to TEXT,
			expires_at REAL,
			status TEXT,
			outcome TEXT,
			reserved_by TEXT,
			filename TEXT
		);
		`,
		// Migration 2: Topics and Cursors
		`
		CREATE TABLE IF NOT EXISTS topics (
			name TEXT PRIMARY KEY
		);

		CREATE TABLE IF NOT EXISTS topic_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic_name TEXT,
			sender TEXT,
			ts REAL,
			body TEXT,
			FOREIGN KEY(topic_name) REFERENCES topics(name)
		);

		CREATE TABLE IF NOT EXISTS topic_cursors (
			agent_name TEXT,
			topic_name TEXT,
			last_read_message_id INTEGER,
			PRIMARY KEY (agent_name, topic_name),
			FOREIGN KEY(topic_name) REFERENCES topics(name)
		);
		`,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentVersion int
	err = tx.QueryRow("SELECT IFNULL(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion)
	if err != nil {
		return err
	}

	now := unixFloat(time.Now().UTC())
	for i, query := range migrations {
		version := i + 1
		if version <= currentVersion {
			continue
		}

		if _, err := tx.Exec(query); err != nil {
			return fmt.Errorf("failed to apply migration version %d: %w", version, err)
		}

		_, err = tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, now)
		if err != nil {
			return fmt.Errorf("failed to log migration version %d: %w", version, err)
		}
	}

	return tx.Commit()
}

func (s *SqliteStore) EnsureActor(actor string) error {
	if err := ValidateName("actor", actor); err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	for _, sub := range []string{"new", "processing", "cur", "expired"} {
		if err := os.MkdirAll(filepath.Join(s.Root, actor, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *SqliteStore) LockActor(actor string) (*ActorLock, error) {
	if err := s.EnsureActor(actor); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, actor, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("actor %q is already locked by another botfam process", actor)
	}
	key := lockKey(s.Root, actor)
	activeLocksMu.Lock()
	activeLocks[key] = true
	activeLocksMu.Unlock()
	return &ActorLock{file: f, key: key}, nil
}

func (s *SqliteStore) IsActorLocked(actor string) bool {
	key := lockKey(s.Root, actor)
	activeLocksMu.Lock()
	defer activeLocksMu.Unlock()
	return activeLocks[key]
}

func (s *SqliteStore) RollbackProcessing(actor string) error {
	if err := s.EnsureActor(actor); err != nil {
		return err
	}
	rows, err := s.db.Query("SELECT id, filename FROM messages WHERE to_actor = ? AND status = 'processing'", actor)
	if err != nil {
		return err
	}
	defer rows.Close()

	type msgInfo struct {
		id       string
		filename string
	}
	var msgs []msgInfo
	for rows.Next() {
		var mi msgInfo
		if err := rows.Scan(&mi.id, &mi.filename); err == nil {
			msgs = append(msgs, mi)
		}
	}

	for _, mi := range msgs {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE messages SET status = 'new' WHERE id = ?", mi.id)
		if err != nil {
			tx.Rollback()
			return err
		}

		src := filepath.Join(s.Root, actor, "processing", mi.filename)
		dst := filepath.Join(s.Root, actor, "new", mi.filename)
		_ = os.Rename(src, dst)

		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func jsonMarshalStr(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func jsonUnmarshalStr(str string, v any) error {
	return json.Unmarshal([]byte(str), v)
}

func (s *SqliteStore) Send(from, to, typ string, payload map[string]any, inReplyTo string, expiresAt *float64) (Message, error) {
	if err := ValidateName("from actor", from); err != nil {
		return Message{}, err
	}
	if err := ValidateName("to actor", to); err != nil {
		return Message{}, err
	}
	if typ == "" {
		return Message{}, errors.New("message type is required")
	}
	if err := s.EnsureActor(to); err != nil {
		return Message{}, err
	}

	now := time.Now().UTC()
	msg := Message{
		ID:        id(),
		From:      from,
		To:        to,
		Type:      typ,
		Payload:   nonnil(payload),
		TS:        unixFloat(now),
		InReplyTo: inReplyTo,
		ExpiresAt: expiresAt,
	}

	payloadStr, err := jsonMarshalStr(msg.Payload)
	if err != nil {
		return Message{}, err
	}

	name := filename(now, msg.ID)
	msg.filename = name

	_, err = s.db.Exec(`
		INSERT INTO messages (id, from_actor, to_actor, type, payload, ts, in_reply_to, expires_at, status, filename)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'new', ?)
	`, msg.ID, msg.From, msg.To, msg.Type, payloadStr, msg.TS, msg.InReplyTo, msg.ExpiresAt, msg.filename)
	if err != nil {
		return Message{}, err
	}

	shadowPath := filepath.Join(s.Root, to, "new", name)
	_ = s.writeJSONAtomic(shadowPath, msg)

	return msg, nil
}

func (s *SqliteStore) expire(actor string) error {
	if err := s.EnsureActor(actor); err != nil {
		return err
	}
	now := unixFloat(time.Now().UTC())
	rows, err := s.db.Query(`
		SELECT id, filename FROM messages 
		WHERE to_actor = ? AND status = 'new' AND expires_at IS NOT NULL AND expires_at <= ?
	`, actor, now)
	if err != nil {
		return err
	}
	defer rows.Close()

	type msgInfo struct {
		id       string
		filename string
	}
	var expiredMsgs []msgInfo
	for rows.Next() {
		var mi msgInfo
		if err := rows.Scan(&mi.id, &mi.filename); err == nil {
			expiredMsgs = append(expiredMsgs, mi)
		}
	}

	for _, mi := range expiredMsgs {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE messages SET status = 'expired' WHERE id = ?", mi.id)
		if err != nil {
			tx.Rollback()
			return err
		}

		src := filepath.Join(s.Root, actor, "new", mi.filename)
		dst := filepath.Join(s.Root, actor, "expired", mi.filename)
		_ = os.Rename(src, dst)

		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SqliteStore) scanMessage(row interface {
	Scan(dest ...any) error
}) (Message, error) {
	var msg Message
	var payloadStr string
	var inReplyTo sql.NullString
	var expiresAt *float64
	var outcomeStr sql.NullString
	var filenameStr sql.NullString

	err := row.Scan(
		&msg.ID, &msg.From, &msg.To, &msg.Type, &payloadStr, &msg.TS,
		&inReplyTo, &expiresAt, &outcomeStr, &filenameStr,
	)
	if err != nil {
		return Message{}, err
	}

	msg.InReplyTo = inReplyTo.String
	msg.ExpiresAt = expiresAt
	if err := jsonUnmarshalStr(payloadStr, &msg.Payload); err != nil {
		return Message{}, err
	}
	if outcomeStr.Valid {
		_ = jsonUnmarshalStr(outcomeStr.String, &msg.Outcome)
	}
	msg.filename = filenameStr.String

	return msg, nil
}

func (s *SqliteStore) TryRecv(actor, matchType string) (*Message, error) {
	if err := s.expire(actor); err != nil {
		return nil, err
	}

	columns := "id, from_actor, to_actor, type, payload, ts, in_reply_to, expires_at, outcome, filename"
	query := "SELECT " + columns + " FROM messages WHERE to_actor = ? AND status = 'new' "
	var args []any
	args = append(args, actor)
	if matchType != "" {
		query += "AND type = ? "
		args = append(args, matchType)
	}
	query += "ORDER BY ts ASC, id ASC LIMIT 1"

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRow(query, args...)
	msg, err := s.scanMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	_, err = tx.Exec("UPDATE messages SET status = 'processing' WHERE id = ?", msg.ID)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, actor, "new", msg.filename)
	dst := filepath.Join(s.Root, actor, "processing", msg.filename)
	_ = os.Rename(src, dst)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &msg, nil
}

func (s *SqliteStore) Recv(ctx context.Context, actor, matchType string, timeout time.Duration) (*Message, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	sweepTick := time.NewTicker(15 * time.Second)
	defer sweepTick.Stop()
	for {
		msg, err := s.TryRecv(actor, matchType)
		if err != nil || msg != nil {
			return msg, err
		}
		if timeout <= 0 || time.Now().After(deadline) {
			return nil, nil
		}
		wait := time.Until(deadline)
		if wait > 200*time.Millisecond {
			wait = 200 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		case <-tick.C:
		case <-sweepTick.C:
			_, _ = s.Sweep()
		}
	}
}

func (s *SqliteStore) Peek(actor, matchType string) (*Message, error) {
	if err := s.expire(actor); err != nil {
		return nil, err
	}

	columns := "id, from_actor, to_actor, type, payload, ts, in_reply_to, expires_at, outcome, filename"
	query := "SELECT " + columns + " FROM messages WHERE to_actor = ? AND status = 'new' "
	var args []any
	args = append(args, actor)
	if matchType != "" {
		query += "AND type = ? "
		args = append(args, matchType)
	}
	query += "ORDER BY ts ASC, id ASC LIMIT 1"

	row := s.db.QueryRow(query, args...)
	msg, err := s.scanMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &msg, nil
}

func (s *SqliteStore) Ack(actor, msgID string, outcome any) (*Message, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}

	columns := "id, from_actor, to_actor, type, payload, ts, in_reply_to, expires_at, outcome, filename"
	query := "SELECT " + columns + " FROM messages WHERE to_actor = ? AND id = ? AND status = 'processing'"
	row := s.db.QueryRow(query, actor, msgID)
	msg, err := s.scanMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("message %q is not reserved by %s", msgID, actor)
		}
		return nil, err
	}

	msg.Outcome = outcome

	outcomeStr := "null"
	if outcome != nil {
		oBytes, err := json.Marshal(outcome)
		if err != nil {
			return nil, err
		}
		outcomeStr = string(oBytes)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE messages SET status = 'cur', outcome = ? WHERE id = ?", outcomeStr, msg.ID)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, actor, "processing", msg.filename)
	dst := filepath.Join(s.Root, actor, "cur", msg.filename)

	if err := s.writeJSONAtomic(src, msg); err != nil {
		return nil, err
	}
	_ = os.Rename(src, dst)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &msg, nil
}

func (s *SqliteStore) Seen(actor, msgID string) (bool, error) {
	if err := s.Init(); err != nil {
		return false, err
	}
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE to_actor = ? AND id = ? AND status = 'cur'", actor, msgID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SqliteStore) Inbox(actor string) (InboxSnapshot, error) {
	if err := s.EnsureActor(actor); err != nil {
		return InboxSnapshot{}, err
	}

	getIDs := func(status string, limit int) ([]string, error) {
		query := "SELECT id, ts FROM messages WHERE to_actor = ? AND status = ? ORDER BY ts ASC, id ASC"
		if limit > 0 {
			query += fmt.Sprintf(" LIMIT %d", limit)
		}
		rows, err := s.db.Query(query, actor, status)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var ids []string
		for rows.Next() {
			var id string
			var ts float64
			if err := rows.Scan(&id, &ts); err == nil {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}

	curIDs, err := getIDs("cur", 0)
	if err != nil {
		return InboxSnapshot{}, err
	}
	if len(curIDs) > 20 {
		curIDs = curIDs[len(curIDs)-20:]
	}

	newIDs, err := getIDs("new", 0)
	if err != nil {
		return InboxSnapshot{}, err
	}

	procIDs, err := getIDs("processing", 0)
	if err != nil {
		return InboxSnapshot{}, err
	}

	counts, err := s.TaskCounts()
	if err != nil {
		return InboxSnapshot{}, err
	}

	return InboxSnapshot{
		Actor:      actor,
		New:        newIDs,
		Processing: procIDs,
		Cur:        curIDs,
		Tasks:      counts,
	}, nil
}

func (s *SqliteStore) Post(actor, typ string, payload map[string]any) (Task, error) {
	if err := ValidateName("actor", actor); err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return Task{}, err
	}
	if typ == "" {
		typ = "task"
	}
	_, _ = s.sweepUnlocked()

	now := time.Now().UTC()
	task := Task{
		ID:        id(),
		Type:      typ,
		Payload:   nonnil(payload),
		Status:    "open",
		CreatedAt: unixFloat(now),
	}

	payloadStr, err := jsonMarshalStr(task.Payload)
	if err != nil {
		return Task{}, err
	}

	name := filename(now, task.ID)
	task.filename = name

	_, err = s.db.Exec(`
		INSERT INTO tasks (id, type, payload, status, created_at, filename)
		VALUES (?, ?, ?, 'open', ?, ?)
	`, task.ID, task.Type, payloadStr, task.CreatedAt, task.filename)
	if err != nil {
		return Task{}, err
	}

	_ = s.writeJSONAtomic(filepath.Join(s.Root, "tasks", "open", name), task)

	return task, nil
}

func (s *SqliteStore) scanTask(row interface {
	Scan(dest ...any) error
}) (Task, error) {
	var task Task
	var payloadStr string
	var owner sql.NullString
	var claimedAt *float64
	var leaseExpiresAt *float64
	var resultStr sql.NullString
	var completedAt *float64
	var abandonedAt *float64
	var abandonedBy sql.NullString
	var abandonedReason sql.NullString
	var sweptAt *float64
	var sweptFrom sql.NullString
	var filenameStr sql.NullString

	err := row.Scan(
		&task.ID, &task.Type, &payloadStr, &task.Status, &owner, &task.CreatedAt,
		&claimedAt, &leaseExpiresAt, &resultStr, &completedAt, &abandonedAt,
		&abandonedBy, &abandonedReason, &sweptAt, &sweptFrom, &filenameStr,
	)
	if err != nil {
		return Task{}, err
	}

	if err := jsonUnmarshalStr(payloadStr, &task.Payload); err != nil {
		return Task{}, err
	}
	task.Owner = owner.String
	task.ClaimedAt = claimedAt
	task.LeaseExpiresAt = leaseExpiresAt
	if resultStr.Valid {
		_ = jsonUnmarshalStr(resultStr.String, &task.Result)
	}
	task.CompletedAt = completedAt
	task.AbandonedAt = abandonedAt
	task.AbandonedBy = abandonedBy.String
	task.AbandonedReason = abandonedReason.String
	task.SweptAt = sweptAt
	task.SweptFrom = sweptFrom.String
	task.filename = filenameStr.String

	return task, nil
}

func (s *SqliteStore) Claim(actor string, leaseTTL time.Duration, opts ClaimOptions) (*Task, error) {
	if err := ValidateName("actor", actor); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return nil, err
	}
	_, _ = s.sweepUnlocked()

	columns := "id, type, payload, status, owner, created_at, claimed_at, lease_expires_at, result, completed_at, abandoned_at, abandoned_by, abandoned_reason, swept_at, swept_from, filename"

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var task Task
	if opts.TaskID != "" {
		row := tx.QueryRow("SELECT "+columns+" FROM tasks WHERE id = ?", opts.TaskID)
		t, err := s.scanTask(row)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("task %q not found", opts.TaskID)
			}
			return nil, err
		}

		if t.Status == "claimed" {
			return nil, fmt.Errorf("task %q is already claimed by %q", opts.TaskID, t.Owner)
		}
		if t.Status == "done" {
			return nil, fmt.Errorf("task %q is already completed", opts.TaskID)
		}
		if !matchFilters(t, opts.Type, opts.SuggestedOwner) {
			return nil, fmt.Errorf("task %q does not match filters", opts.TaskID)
		}
		task = t
	} else {
		query := "SELECT " + columns + " FROM tasks WHERE status = 'open' ORDER BY created_at ASC, id ASC"
		rows, err := tx.Query(query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		found := false
		for rows.Next() {
			t, err := s.scanTask(rows)
			if err != nil {
				continue
			}
			if matchFilters(t, opts.Type, opts.SuggestedOwner) {
				task = t
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}

	now := unixFloat(time.Now().UTC())
	exp := unixFloat(time.Now().UTC().Add(leaseTTL))

	task.Status = "claimed"
	task.Owner = actor
	task.ClaimedAt = &now
	task.LeaseExpiresAt = &exp

	_, err = tx.Exec(`
		UPDATE tasks 
		SET status = 'claimed', owner = ?, claimed_at = ?, lease_expires_at = ?
		WHERE id = ? AND status = 'open'
	`, task.Owner, task.ClaimedAt, task.LeaseExpiresAt, task.ID)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, "tasks", "open", task.filename)
	dst := filepath.Join(s.Root, "tasks", "claimed", actor, task.filename)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := s.writeJSONAtomic(src, task); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &task, nil
}

func (s *SqliteStore) findClaimed(actor, taskID string) (Task, error) {
	if err := s.initUnlocked(); err != nil {
		return Task{}, err
	}
	columns := "id, type, payload, status, owner, created_at, claimed_at, lease_expires_at, result, completed_at, abandoned_at, abandoned_by, abandoned_reason, swept_at, swept_from, filename"
	row := s.db.QueryRow("SELECT "+columns+" FROM tasks WHERE id = ? AND owner = ? AND status = 'claimed'", taskID, actor)
	task, err := s.scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task %q is not claimed by %s", taskID, actor)
		}
		return Task{}, err
	}
	return task, nil
}

func (s *SqliteStore) Complete(actor, taskID string, result any) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.findClaimed(actor, taskID)
	if err != nil {
		return nil, err
	}

	now := unixFloat(time.Now().UTC())
	task.Status = "done"
	task.Result = result
	task.CompletedAt = &now

	resultStr := "null"
	if result != nil {
		rBytes, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		resultStr = string(rBytes)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE tasks 
		SET status = 'done', result = ?, completed_at = ?
		WHERE id = ? AND owner = ? AND status = 'claimed'
	`, resultStr, task.CompletedAt, task.ID, actor)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, "tasks", "claimed", actor, task.filename)
	dst := filepath.Join(s.Root, "tasks", "done", task.filename)

	if err := s.writeJSONAtomic(src, task); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &task, nil
}

func (s *SqliteStore) Heartbeat(actor, taskID string, leaseTTL time.Duration) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.findClaimed(actor, taskID)
	if err != nil {
		return nil, err
	}

	exp := unixFloat(time.Now().UTC().Add(leaseTTL))
	task.LeaseExpiresAt = &exp

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE tasks 
		SET lease_expires_at = ?
		WHERE id = ? AND owner = ? AND status = 'claimed'
	`, task.LeaseExpiresAt, task.ID, actor)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, "tasks", "claimed", actor, task.filename)
	if err := s.writeJSONAtomic(src, task); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &task, nil
}

func (s *SqliteStore) Abandon(actor, taskID, reason string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, err := s.findClaimed(actor, taskID)
	if err != nil {
		return nil, err
	}

	now := unixFloat(time.Now().UTC())
	task.Status = "open"
	task.Owner = ""
	task.LeaseExpiresAt = nil
	task.AbandonedAt = &now
	task.AbandonedBy = actor
	task.AbandonedReason = reason

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE tasks 
		SET status = 'open', owner = NULL, lease_expires_at = NULL,
		    abandoned_at = ?, abandoned_by = ?, abandoned_reason = ?
		WHERE id = ? AND owner = ? AND status = 'claimed'
	`, task.AbandonedAt, task.AbandonedBy, task.AbandonedReason, task.ID, actor)
	if err != nil {
		return nil, err
	}

	src := filepath.Join(s.Root, "tasks", "claimed", actor, task.filename)
	dst := filepath.Join(s.Root, "tasks", "open", task.filename)

	if err := s.writeJSONAtomic(src, task); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &task, nil
}

func (s *SqliteStore) reapStaleTmpFiles() error {
	tmpDir := filepath.Join(s.Root, "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	now := time.Now().UTC()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.Contains(name, ".json.tmp-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Stale if older than 5 minutes
		if now.Sub(info.ModTime().UTC()) < 5*time.Minute {
			continue
		}
		// Reclaim task
		src := filepath.Join(tmpDir, name)
		parts := strings.Split(name, ".json.tmp-")
		if len(parts) < 1 {
			continue
		}
		f := parts[0] + ".json"
		task, err := readTask(src)
		if err != nil {
			// If completely unparseable, remove it to avoid leaking
			_ = os.Remove(src)
			continue
		}
		if task.ID == "" || (task.Status != "open" && task.Status != "claimed" && task.Status != "done") {
			_ = os.Remove(src)
			continue
		}

		var dst string
		if task.Status == "done" {
			dst = filepath.Join(s.Root, "tasks", "done", f)
			resStr := "null"
			if task.Result != nil {
				if rBytes, err := json.Marshal(task.Result); err == nil {
					resStr = string(rBytes)
				}
			}
			_, _ = s.db.Exec(`
				INSERT INTO tasks (id, type, payload, status, owner, created_at, claimed_at, lease_expires_at, result, completed_at, filename)
				VALUES (?, ?, ?, 'done', ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET status='done', result=excluded.result, completed_at=excluded.completed_at
			`, task.ID, task.Type, "{}", task.Owner, task.CreatedAt, task.ClaimedAt, task.LeaseExpiresAt, resStr, task.CompletedAt, f)
		} else {
			task.Status = "open"
			task.Owner = ""
			task.LeaseExpiresAt = nil
			sweptAt := unixFloat(now)
			task.SweptAt = &sweptAt
			task.SweptFrom = "tmp-recovery"
			if err := writeJSON(src, task); err != nil {
				continue
			}
			dst = filepath.Join(s.Root, "tasks", "open", f)
			_, _ = s.db.Exec(`
				INSERT INTO tasks (id, type, payload, status, owner, created_at, swept_at, swept_from, filename)
				VALUES (?, ?, ?, 'open', NULL, ?, ?, 'tmp-recovery', ?)
				ON CONFLICT(id) DO UPDATE SET status='open', owner=NULL, lease_expires_at=NULL, swept_at=excluded.swept_at, swept_from='tmp-recovery'
			`, task.ID, task.Type, "{}", task.CreatedAt, sweptAt, f)
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		_ = os.Rename(src, dst)
	}
	return nil
}

func (s *SqliteStore) Sweep() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sweepUnlocked()
}

func (s *SqliteStore) sweepUnlocked() ([]Task, error) {
	if err := s.initUnlocked(); err != nil {
		return nil, err
	}
	_ = s.reapStaleTmpFiles()

	columns := "id, type, payload, status, owner, created_at, claimed_at, lease_expires_at, result, completed_at, abandoned_at, abandoned_by, abandoned_reason, swept_at, swept_from, filename"
	now := unixFloat(time.Now().UTC())

	rows, err := s.db.Query("SELECT "+columns+" FROM tasks WHERE status = 'claimed' AND lease_expires_at <= ?", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var expiredTasks []Task
	for rows.Next() {
		t, err := s.scanTask(rows)
		if err == nil {
			expiredTasks = append(expiredTasks, t)
		}
	}

	var swept []Task
	for _, task := range expiredTasks {
		tx, err := s.db.Begin()
		if err != nil {
			return swept, err
		}

		sweptAt := unixFloat(time.Now().UTC())
		sweptFrom := task.Owner

		task.Status = "open"
		task.Owner = ""
		task.LeaseExpiresAt = nil
		task.SweptAt = &sweptAt
		task.SweptFrom = sweptFrom

		_, err = tx.Exec(`
			UPDATE tasks 
			SET status = 'open', owner = NULL, lease_expires_at = NULL,
			    swept_at = ?, swept_from = ?
			WHERE id = ? AND status = 'claimed' AND lease_expires_at <= ?
		`, sweptAt, sweptFrom, task.ID, now)
		if err != nil {
			tx.Rollback()
			continue
		}

		src := filepath.Join(s.Root, "tasks", "claimed", sweptFrom, task.filename)
		dst := filepath.Join(s.Root, "tasks", "open", task.filename)

		if err := s.writeJSONAtomic(src, task); err != nil {
			tx.Rollback()
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			tx.Rollback()
			continue
		}
		_ = os.Rename(src, dst)

		if err := tx.Commit(); err == nil {
			swept = append(swept, task)
		}
	}

	return swept, nil
}

func (s *SqliteStore) TaskCounts() (TaskCounts, error) {
	if err := s.Init(); err != nil {
		return TaskCounts{}, err
	}
	var openCount int
	err := s.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'open'").Scan(&openCount)
	if err != nil {
		return TaskCounts{}, err
	}

	var doneCount int
	err = s.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'done'").Scan(&doneCount)
	if err != nil {
		return TaskCounts{}, err
	}

	rows, err := s.db.Query("SELECT owner, COUNT(*) FROM tasks WHERE status = 'claimed' GROUP BY owner")
	if err != nil {
		return TaskCounts{}, err
	}
	defer rows.Close()

	claimed := make(map[string]int)
	for rows.Next() {
		var owner string
		var count int
		if err := rows.Scan(&owner, &count); err == nil {
			claimed[owner] = count
		}
	}

	return TaskCounts{
		Open:    openCount,
		Claimed: claimed,
		Done:    doneCount,
	}, nil
}

func (s *SqliteStore) SessionNew(slug string, participants []string, creator string, decisionRule string, goals []string, guardrails []string) error {
	if err := ValidateName("session slug", slug); err != nil {
		return err
	}
	for _, p := range participants {
		if err := ValidateName("participant", p); err != nil {
			return err
		}
	}
	if err := s.Init(); err != nil {
		return err
	}

	// Try to ensure session is in DB. If it succeeds, it means it already exists (or was imported).
	if err := s.ensureSessionInDB(slug); err == nil {
		return fmt.Errorf("session %q already exists", slug)
	} else if !strings.Contains(err.Error(), "does not exist") {
		return err
	}

	participantsStr, _ := jsonMarshalStr(participants)
	goalsStr, _ := jsonMarshalStr(goals)
	guardrailsStr, _ := jsonMarshalStr(guardrails)
	now := unixFloat(time.Now().UTC())

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO sessions (slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0)
	`, slug, participantsStr, creator, now, decisionRule, goalsStr, guardrailsStr)
	if err != nil {
		return err
	}

	dir := filepath.Join(s.Root, "sessions", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	metaPath := filepath.Join(dir, "meta.json")
	meta := SessionMeta{
		Slug:         slug,
		Participants: participants,
		CreatedBy:    creator,
		CreatedAt:    now,
		DecisionRule: decisionRule,
		Goals:        goals,
		Guardrails:   guardrails,
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(metaPath, b, 0o644); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SqliteStore) SessionAppend(slug, actor, body string, handoff *SessionHandoff) (SessionEntry, error) {
	if err := ValidateName("session slug", slug); err != nil {
		return SessionEntry{}, err
	}
	if err := ValidateName("actor", actor); err != nil {
		return SessionEntry{}, err
	}
	if !s.IsActorLocked(actor) {
		return SessionEntry{}, fmt.Errorf("actor %q is not locked by this process", actor)
	}
	if handoff != nil {
		if strings.TrimSpace(handoff.Task) == "" {
			return SessionEntry{}, errors.New("invalid handoff: task cannot be empty or whitespace only")
		}
		if strings.TrimSpace(handoff.Context) == "" {
			return SessionEntry{}, errors.New("invalid handoff: context cannot be empty or whitespace only")
		}
		if strings.TrimSpace(handoff.Deliverable) == "" {
			return SessionEntry{}, errors.New("invalid handoff: deliverable cannot be empty or whitespace only")
		}
	}
	if err := s.Init(); err != nil {
		return SessionEntry{}, err
	}

	if err := s.ensureSessionInDB(slug); err != nil {
		return SessionEntry{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return SessionEntry{}, err
	}
	defer tx.Rollback()

	var archived int
	err = tx.QueryRow("SELECT archived FROM sessions WHERE slug = ?", slug).Scan(&archived)
	if err != nil {
		return SessionEntry{}, err
	}
	if archived != 0 {
		return SessionEntry{}, fmt.Errorf("session %q is archived and read-only", slug)
	}

	entry := SessionEntry{
		ID:      id(),
		Actor:   actor,
		TS:      unixFloat(time.Now().UTC()),
		Body:    body,
		Handoff: handoff,
	}

	var handoffTask, handoffCtx, handoffDel sql.NullString
	if handoff != nil {
		handoffTask = sql.NullString{String: handoff.Task, Valid: true}
		handoffCtx = sql.NullString{String: handoff.Context, Valid: true}
		handoffDel = sql.NullString{String: handoff.Deliverable, Valid: true}
	}

	_, err = tx.Exec(`
		INSERT INTO session_entries (id, session_slug, actor, ts, body, handoff_task, handoff_context, handoff_deliverable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.ID, slug, entry.Actor, entry.TS, entry.Body, handoffTask, handoffCtx, handoffDel)
	if err != nil {
		return SessionEntry{}, err
	}

	dir := filepath.Join(s.Root, "sessions", slug)
	line, err := json.Marshal(entry)
	if err != nil {
		return SessionEntry{}, err
	}
	line = append(line, '\n')

	jsonlPath := filepath.Join(dir, "session.jsonl")
	f, err := os.OpenFile(jsonlPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return SessionEntry{}, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return SessionEntry{}, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(line); err != nil {
		return SessionEntry{}, err
	}
	if err := f.Sync(); err != nil {
		return SessionEntry{}, err
	}

	if err := tx.Commit(); err != nil {
		return SessionEntry{}, err
	}

	return entry, nil
}

func (s *SqliteStore) SessionRead(slug, actor string, sinceTS float64, limit int) ([]SessionEntry, error) {
	if err := ValidateName("session slug", slug); err != nil {
		return nil, err
	}
	if err := s.Init(); err != nil {
		return nil, err
	}

	if err := s.ensureSessionInDB(slug); err != nil {
		return nil, err
	}

	query := "SELECT id, actor, ts, body, handoff_task, handoff_context, handoff_deliverable FROM session_entries WHERE session_slug = ? "
	var args []any
	args = append(args, slug)

	if actor != "" {
		query += "AND actor = ? "
		args = append(args, actor)
	}
	if sinceTS > 0 {
		query += "AND ts > ? "
		args = append(args, sinceTS)
	}

	query += "ORDER BY ts ASC, id ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []SessionEntry
	for rows.Next() {
		var entry SessionEntry
		var handoffTask, handoffCtx, handoffDel sql.NullString
		err := rows.Scan(
			&entry.ID, &entry.Actor, &entry.TS, &entry.Body,
			&handoffTask, &handoffCtx, &handoffDel,
		)
		if err != nil {
			return nil, err
		}

		if handoffTask.Valid || handoffCtx.Valid || handoffDel.Valid {
			entry.Handoff = &SessionHandoff{
				Task:        handoffTask.String,
				Context:     handoffCtx.String,
				Deliverable: handoffDel.String,
			}
		}
		entries = append(entries, entry)
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, nil
}

func (s *SqliteStore) ensureSessionInDB(slug string) error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE slug = ?", slug).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	dir := filepath.Join(s.Root, "sessions", slug)
	metaPath := filepath.Join(dir, "meta.json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			jsonlPath := filepath.Join(dir, "session.jsonl")
			if _, statErr := os.Stat(jsonlPath); statErr != nil {
				return fmt.Errorf("session %q does not exist", slug)
			}
			tx, err := s.db.Begin()
			if err != nil {
				return err
			}
			defer tx.Rollback()
			_, err = tx.Exec(`
				INSERT OR IGNORE INTO sessions (slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived)
				VALUES (?, '[]', '', ?, '', '[]', '[]', 0)
			`, slug, unixFloat(time.Now()))
			if err != nil {
				return err
			}
			if err := s.importSessionEntries(tx, slug); err != nil {
				return err
			}
			return tx.Commit()
		}
		return err
	}

	var meta SessionMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("failed to parse meta.json for session %q: %w", slug, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	participantsStr, _ := jsonMarshalStr(nonnilSlice(meta.Participants))
	goalsStr, _ := jsonMarshalStr(nonnilSlice(meta.Goals))
	guardrailsStr, _ := jsonMarshalStr(nonnilSlice(meta.Guardrails))
	archivedVal := 0
	if meta.Archived {
		archivedVal = 1
	}

	_, err = tx.Exec(`
		INSERT OR IGNORE INTO sessions (slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, meta.Slug, participantsStr, meta.CreatedBy, meta.CreatedAt, meta.DecisionRule, goalsStr, guardrailsStr, archivedVal)
	if err != nil {
		return err
	}

	if err := s.importSessionEntries(tx, slug); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SqliteStore) importSessionEntries(tx *sql.Tx, slug string) error {
	jsonlPath := filepath.Join(s.Root, "sessions", slug, "session.jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			continue
		}

		var handoffTask, handoffCtx, handoffDel sql.NullString
		if entry.Handoff != nil {
			handoffTask = sql.NullString{String: entry.Handoff.Task, Valid: true}
			handoffCtx = sql.NullString{String: entry.Handoff.Context, Valid: true}
			handoffDel = sql.NullString{String: entry.Handoff.Deliverable, Valid: true}
		}

		_, err = tx.Exec(`
			INSERT OR IGNORE INTO session_entries (id, session_slug, actor, ts, body, handoff_task, handoff_context, handoff_deliverable)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, entry.ID, slug, entry.Actor, entry.TS, entry.Body, handoffTask, handoffCtx, handoffDel)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *SqliteStore) syncSessionsFromDisk() error {
	dir := filepath.Join(s.Root, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			slug := entry.Name()
			_ = s.ensureSessionInDB(slug)
		}
	}
	return nil
}

func nonnilSlice(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func (s *SqliteStore) scanSession(row interface {
	Scan(dest ...any) error
}) (SessionMeta, error) {
	var meta SessionMeta
	var participantsStr string
	var goalsStr string
	var guardrailsStr string
	var archived int

	err := row.Scan(
		&meta.Slug, &participantsStr, &meta.CreatedBy, &meta.CreatedAt,
		&meta.DecisionRule, &goalsStr, &guardrailsStr, &archived,
	)
	if err != nil {
		return SessionMeta{}, err
	}

	_ = jsonUnmarshalStr(participantsStr, &meta.Participants)
	_ = jsonUnmarshalStr(goalsStr, &meta.Goals)
	_ = jsonUnmarshalStr(guardrailsStr, &meta.Guardrails)
	meta.Archived = (archived != 0)

	return meta, nil
}

func (s *SqliteStore) SessionList() ([]SessionMeta, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	if err := s.syncSessionsFromDisk(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query("SELECT slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived FROM sessions WHERE archived = 0 ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var active []SessionMeta
	for rows.Next() {
		meta, err := s.scanSession(rows)
		if err != nil {
			continue
		}
		tombstone := filepath.Join(s.Root, "sessions", meta.Slug, "ARCHIVED")
		if _, err := os.Stat(tombstone); err == nil {
			_, _ = s.db.Exec("UPDATE sessions SET archived = 1 WHERE slug = ?", meta.Slug)
			continue
		}
		active = append(active, meta)
	}
	return active, nil
}

func (s *SqliteStore) SessionListAll() ([]SessionMeta, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}
	if err := s.syncSessionsFromDisk(); err != nil {
		return nil, err
	}

	rows, err := s.db.Query("SELECT slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived FROM sessions ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []SessionMeta
	for rows.Next() {
		meta, err := s.scanSession(rows)
		if err != nil {
			continue
		}
		tombstone := filepath.Join(s.Root, "sessions", meta.Slug, "ARCHIVED")
		if _, err := os.Stat(tombstone); err == nil {
			meta.Archived = true
			_, _ = s.db.Exec("UPDATE sessions SET archived = 1 WHERE slug = ?", meta.Slug)
		}
		all = append(all, meta)
	}
	return all, nil
}

func (s *SqliteStore) SessionRender(slug string) (string, error) {
	entries, err := s.SessionRead(slug, "", 0, 0)
	if err != nil {
		return "", err
	}

	var meta SessionMeta
	var participantsStr, goalsStr, guardrailsStr string
	var archived int
	err = s.db.QueryRow(`
		SELECT slug, participants, created_by, created_at, decision_rule, goals, guardrails, archived 
		FROM sessions WHERE slug = ?
	`, slug).Scan(&meta.Slug, &participantsStr, &meta.CreatedBy, &meta.CreatedAt, &meta.DecisionRule, &goalsStr, &guardrailsStr, &archived)
	if err != nil {
		return "", err
	}
	_ = jsonUnmarshalStr(participantsStr, &meta.Participants)
	_ = jsonUnmarshalStr(goalsStr, &meta.Goals)
	_ = jsonUnmarshalStr(guardrailsStr, &meta.Guardrails)

	var out strings.Builder
	out.WriteString("<!-- RENDERED by botfam session render — DO NOT EDIT (append via session_append) -->\n\n")
	out.WriteString(fmt.Sprintf("# Session: %s\n\n", slug))
	out.WriteString("## Participants\n\n")
	for _, p := range meta.Participants {
		out.WriteString(fmt.Sprintf("- %s\n", p))
	}
	out.WriteString("\n---\n")

	for _, entry := range entries {
		out.WriteString("\n")
		t := time.Unix(0, int64(entry.TS*1e9)).UTC()
		out.WriteString(fmt.Sprintf("## [%s, %s]\n", entry.Actor, t.Format(time.RFC3339)))
		out.WriteString(strings.TrimSpace(entry.Body))
		out.WriteString("\n")

		if entry.Handoff != nil {
			out.WriteString("\n**→ Handoff:**\n")
			out.WriteString(fmt.Sprintf("**Task:** %s\n", entry.Handoff.Task))
			out.WriteString(fmt.Sprintf("**Context:** %s\n", entry.Handoff.Context))
			out.WriteString(fmt.Sprintf("**Deliverable:** %s\n", entry.Handoff.Deliverable))
		}
	}

	return out.String(), nil
}

func (s *SqliteStore) SessionClose(slug, repoRoot string) error {
	rendered, err := s.SessionRender(slug)
	if err != nil {
		return err
	}

	destDir := filepath.Join(repoRoot, "doc", "collab", "sessions", slug)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	destFile := filepath.Join(destDir, "session.md")
	if err := os.WriteFile(destFile, []byte(rendered), 0o644); err != nil {
		return err
	}

	now := unixFloat(time.Now().UTC())
	_, err = s.db.Exec("UPDATE sessions SET archived = 1, closed_at = ? WHERE slug = ?", now, slug)
	if err != nil {
		return err
	}

	tombstonePath := filepath.Join(s.Root, "sessions", slug, "ARCHIVED")
	return os.WriteFile(tombstonePath, nil, 0o644)
}

func (s *SqliteStore) writeJSONAtomic(dst string, v any) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "tmp"), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(s.Root, "tmp", filepath.Base(dst)+".tmp-"+id())
	if err := writeJSON(tmp, v); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

var topicNameRE = regexp.MustCompile(`^[#A-Za-z0-9_-]+$`)

func validateTopicName(name string) error {
	if !topicNameRE.MatchString(name) {
		return fmt.Errorf("invalid topic name %q: must match [#A-Za-z0-9_-]+", name)
	}
	return nil
}

func (s *SqliteStore) TopicPublish(topic, sender, body string) (TopicMessage, error) {
	if err := validateTopicName(topic); err != nil {
		return TopicMessage{}, err
	}
	if err := ValidateName("sender", sender); err != nil {
		return TopicMessage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return TopicMessage{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return TopicMessage{}, err
	}
	defer tx.Rollback()

	_, err = tx.Exec("INSERT OR IGNORE INTO topics (name) VALUES (?)", topic)
	if err != nil {
		return TopicMessage{}, err
	}

	now := unixFloat(time.Now().UTC())
	res, err := tx.Exec(`
		INSERT INTO topic_messages (topic_name, sender, ts, body)
		VALUES (?, ?, ?, ?)
	`, topic, sender, now, body)
	if err != nil {
		return TopicMessage{}, err
	}

	msgID, err := res.LastInsertId()
	if err != nil {
		return TopicMessage{}, err
	}

	msg := TopicMessage{
		ID:    msgID,
		Topic: topic,
		From:  sender,
		TS:    now,
		Body:  body,
	}

	dir := filepath.Join(s.Root, "topics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return TopicMessage{}, err
	}
	jsonlPath := filepath.Join(dir, topic+".jsonl")

	line, err := json.Marshal(msg)
	if err != nil {
		return TopicMessage{}, err
	}
	line = append(line, '\n')

	f, err := os.OpenFile(jsonlPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return TopicMessage{}, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return TopicMessage{}, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(line); err != nil {
		return TopicMessage{}, err
	}
	if err := f.Sync(); err != nil {
		return TopicMessage{}, err
	}

	if err := tx.Commit(); err != nil {
		return TopicMessage{}, err
	}

	return msg, nil
}

func (s *SqliteStore) TopicRead(topic string, sinceID int64, limit int) ([]TopicMessage, error) {
	if err := validateTopicName(topic); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return nil, err
	}

	query := "SELECT id, topic_name, sender, ts, body FROM topic_messages WHERE topic_name = ? AND id > ? ORDER BY id ASC"
	var args []any
	args = append(args, topic, sinceID)

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []TopicMessage
	for rows.Next() {
		var msg TopicMessage
		err := rows.Scan(&msg.ID, &msg.Topic, &msg.From, &msg.TS, &msg.Body)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func (s *SqliteStore) TopicCursorUpdate(agent, topic string, lastReadID int64) error {
	if err := ValidateName("agent", agent); err != nil {
		return err
	}
	if err := validateTopicName(topic); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("INSERT OR IGNORE INTO topics (name) VALUES (?)", topic)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO topic_cursors (agent_name, topic_name, last_read_message_id)
		VALUES (?, ?, ?)
		ON CONFLICT(agent_name, topic_name) DO UPDATE SET last_read_message_id = excluded.last_read_message_id
	`, agent, topic, lastReadID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SqliteStore) TopicCursorRead(agent, topic string) (int64, error) {
	if err := ValidateName("agent", agent); err != nil {
		return 0, err
	}
	if err := validateTopicName(topic); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.initUnlocked(); err != nil {
		return 0, err
	}

	var lastReadID int64
	err := s.db.QueryRow("SELECT last_read_message_id FROM topic_cursors WHERE agent_name = ? AND topic_name = ?", agent, topic).Scan(&lastReadID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return lastReadID, nil
}
