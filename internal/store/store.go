package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

type Store struct {
	Root string
}

type ActorLock struct {
	file *os.File
}

func New(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) Init() error {
	for _, dir := range []string{"tmp", "tasks/open", "tasks/claimed", "tasks/done"} {
		if err := os.MkdirAll(filepath.Join(s.Root, dir), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EnsureActor(actor string) error {
	if err := ValidateName("actor", actor); err != nil {
		return err
	}
	for _, sub := range []string{"new", "processing", "cur", "expired"} {
		if err := os.MkdirAll(filepath.Join(s.Root, actor, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LockActor(actor string) (*ActorLock, error) {
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
	return &ActorLock{file: f}, nil
}

func (l *ActorLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

func (s *Store) RollbackProcessing(actor string) error {
	if err := s.EnsureActor(actor); err != nil {
		return err
	}
	files, err := listJSON(filepath.Join(s.Root, actor, "processing"))
	if err != nil {
		return err
	}
	for _, f := range files {
		_ = os.Rename(filepath.Join(s.Root, actor, "processing", f), filepath.Join(s.Root, actor, "new", f))
	}
	return nil
}

func (s *Store) Send(from, to, typ string, payload map[string]any, inReplyTo string, expiresAt *float64) (Message, error) {
	if err := ValidateName("from actor", from); err != nil {
		return Message{}, err
	}
	if err := ValidateName("to actor", to); err != nil {
		return Message{}, err
	}
	if typ == "" {
		return Message{}, errors.New("message type is required")
	}
	if err := s.Init(); err != nil {
		return Message{}, err
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
	name := filename(now, msg.ID)
	if err := s.writeJSONAtomic(filepath.Join(s.Root, to, "new", name), msg); err != nil {
		return Message{}, err
	}
	msg.filename = name
	return msg, nil
}

func (s *Store) TryRecv(actor, matchType string) (*Message, error) {
	if err := s.expire(actor); err != nil {
		return nil, err
	}
	files, err := listJSON(filepath.Join(s.Root, actor, "new"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		src := filepath.Join(s.Root, actor, "new", f)
		msg, err := readMessage(src)
		if err != nil {
			continue
		}
		if matchType != "" && msg.Type != matchType {
			continue
		}
		dst := filepath.Join(s.Root, actor, "processing", f)
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		msg.filename = f
		return &msg, nil
	}
	return nil, nil
}

func (s *Store) Recv(ctx context.Context, actor, matchType string, timeout time.Duration) (*Message, error) {
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

func (s *Store) Peek(actor, matchType string) (*Message, error) {
	if err := s.expire(actor); err != nil {
		return nil, err
	}
	files, err := listJSON(filepath.Join(s.Root, actor, "new"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		msg, err := readMessage(filepath.Join(s.Root, actor, "new", f))
		if err != nil {
			continue
		}
		if matchType == "" || msg.Type == matchType {
			msg.filename = f
			return &msg, nil
		}
	}
	return nil, nil
}

func (s *Store) Ack(actor, msgID string, outcome any) (*Message, error) {
	files, err := listJSON(filepath.Join(s.Root, actor, "processing"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		src := filepath.Join(s.Root, actor, "processing", f)
		msg, err := readMessage(src)
		if err != nil || msg.ID != msgID {
			continue
		}
		if outcome != nil {
			if msg.Payload == nil {
				msg.Payload = map[string]any{}
			}
			msg.Payload["_ack_outcome"] = outcome
		}
		if err := writeJSON(src, msg); err != nil {
			return nil, err
		}
		dst := filepath.Join(s.Root, actor, "cur", f)
		if err := os.Rename(src, dst); err != nil {
			return nil, err
		}
		return &msg, nil
	}
	return nil, fmt.Errorf("message %q is not reserved by %s", msgID, actor)
}

func (s *Store) Seen(actor, msgID string) (bool, error) {
	files, err := listJSON(filepath.Join(s.Root, actor, "cur"))
	if err != nil {
		return false, err
	}
	for _, f := range files {
		msg, err := readMessage(filepath.Join(s.Root, actor, "cur", f))
		if err == nil && msg.ID == msgID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) Inbox(actor string) (InboxSnapshot, error) {
	if err := s.EnsureActor(actor); err != nil {
		return InboxSnapshot{}, err
	}
	cur, err := listJSON(filepath.Join(s.Root, actor, "cur"))
	if err != nil {
		return InboxSnapshot{}, err
	}
	if len(cur) > 20 {
		cur = cur[len(cur)-20:]
	}
	newFiles, _ := listJSON(filepath.Join(s.Root, actor, "new"))
	proc, _ := listJSON(filepath.Join(s.Root, actor, "processing"))
	counts, err := s.TaskCounts()
	if err != nil {
		return InboxSnapshot{}, err
	}
	return InboxSnapshot{Actor: actor, New: idsFromFiles(newFiles), Processing: idsFromFiles(proc), Cur: idsFromFiles(cur), Tasks: counts}, nil
}

func (s *Store) Post(actor, typ string, payload map[string]any) (Task, error) {
	if err := ValidateName("actor", actor); err != nil {
		return Task{}, err
	}
	if typ == "" {
		typ = "task"
	}
	_, _ = s.Sweep()
	now := time.Now().UTC()
	task := Task{ID: id(), Type: typ, Payload: nonnil(payload), Status: "open", CreatedAt: unixFloat(now)}
	name := filename(now, task.ID)
	if err := s.writeJSONAtomic(filepath.Join(s.Root, "tasks", "open", name), task); err != nil {
		return Task{}, err
	}
	task.filename = name
	return task, nil
}

func (s *Store) Claim(actor string, leaseTTL time.Duration) (*Task, error) {
	if err := ValidateName("actor", actor); err != nil {
		return nil, err
	}
	_, _ = s.Sweep()
	if err := os.MkdirAll(filepath.Join(s.Root, "tasks", "claimed", actor), 0o755); err != nil {
		return nil, err
	}
	files, err := listJSON(filepath.Join(s.Root, "tasks", "open"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		src := filepath.Join(s.Root, "tasks", "open", f)
		dst := filepath.Join(s.Root, "tasks", "claimed", actor, f)
		if err := os.Rename(src, dst); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		task, err := readTask(dst)
		if err != nil {
			return nil, err
		}
		now := unixFloat(time.Now().UTC())
		exp := unixFloat(time.Now().UTC().Add(leaseTTL))
		task.Status = "claimed"
		task.Owner = actor
		task.ClaimedAt = &now
		task.LeaseExpiresAt = &exp
		if err := writeJSON(dst, task); err != nil {
			return nil, err
		}
		task.filename = f
		return &task, nil
	}
	return nil, nil
}

func (s *Store) Complete(actor, taskID string, result any) (*Task, error) {
	path, f, task, err := s.findClaimed(actor, taskID)
	if err != nil {
		return nil, err
	}
	now := unixFloat(time.Now().UTC())
	task.Status = "done"
	task.Result = result
	task.CompletedAt = &now
	if err := writeJSON(path, task); err != nil {
		return nil, err
	}
	dst := filepath.Join(s.Root, "tasks", "done", f)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(path, dst); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) Heartbeat(actor, taskID string, leaseTTL time.Duration) (*Task, error) {
	path, _, task, err := s.findClaimed(actor, taskID)
	if err != nil {
		return nil, err
	}
	exp := unixFloat(time.Now().UTC().Add(leaseTTL))
	task.LeaseExpiresAt = &exp
	return &task, writeJSON(path, task)
}

func (s *Store) Abandon(actor, taskID, reason string) (*Task, error) {
	path, f, task, err := s.findClaimed(actor, taskID)
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
	if err := writeJSON(path, task); err != nil {
		return nil, err
	}
	dst := filepath.Join(s.Root, "tasks", "open", f)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(path, dst); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) Sweep() ([]Task, error) {
	root := filepath.Join(s.Root, "tasks", "claimed")
	actors, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	now := unixFloat(time.Now().UTC())
	out := []Task{}
	for _, actorDir := range actors {
		if !actorDir.IsDir() {
			continue
		}
		actor := actorDir.Name()
		files, _ := listJSON(filepath.Join(root, actor))
		for _, f := range files {
			path := filepath.Join(root, actor, f)
			task, err := readTask(path)
			if err != nil || task.LeaseExpiresAt == nil || *task.LeaseExpiresAt > now {
				continue
			}
			task.Status = "open"
			task.SweptAt = &now
			task.SweptFrom = actor
			task.Owner = ""
			task.LeaseExpiresAt = nil
			if err := writeJSON(path, task); err != nil {
				return out, err
			}
			dst := filepath.Join(s.Root, "tasks", "open", f)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return out, err
			}
			if err := os.Rename(path, dst); err != nil {
				return out, err
			}
			out = append(out, task)
		}
	}
	return out, nil
}

func (s *Store) TaskCounts() (TaskCounts, error) {
	open, _ := listJSON(filepath.Join(s.Root, "tasks", "open"))
	done, _ := listJSON(filepath.Join(s.Root, "tasks", "done"))
	claimed := map[string]int{}
	actors, _ := os.ReadDir(filepath.Join(s.Root, "tasks", "claimed"))
	for _, a := range actors {
		if a.IsDir() {
			files, _ := listJSON(filepath.Join(s.Root, "tasks", "claimed", a.Name()))
			claimed[a.Name()] = len(files)
		}
	}
	return TaskCounts{Open: len(open), Claimed: claimed, Done: len(done)}, nil
}

func (s *Store) findClaimed(actor, taskID string) (string, string, Task, error) {
	files, err := listJSON(filepath.Join(s.Root, "tasks", "claimed", actor))
	if err != nil {
		return "", "", Task{}, err
	}
	for _, f := range files {
		path := filepath.Join(s.Root, "tasks", "claimed", actor, f)
		task, err := readTask(path)
		if err == nil && task.ID == taskID {
			return path, f, task, nil
		}
	}
	return "", "", Task{}, fmt.Errorf("task %q is not claimed by %s", taskID, actor)
}

func (s *Store) expire(actor string) error {
	if err := s.EnsureActor(actor); err != nil {
		return err
	}
	files, err := listJSON(filepath.Join(s.Root, actor, "new"))
	if err != nil {
		return err
	}
	now := unixFloat(time.Now().UTC())
	for _, f := range files {
		path := filepath.Join(s.Root, actor, "new", f)
		msg, err := readMessage(path)
		if err != nil || msg.ExpiresAt == nil || *msg.ExpiresAt > now {
			continue
		}
		_ = os.Rename(path, filepath.Join(s.Root, actor, "expired", f))
	}
	return nil
}

func (s *Store) writeJSONAtomic(dst string, v any) error {
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

func readMessage(path string) (Message, error) {
	var msg Message
	return msg, readJSON(path, &msg)
}

func readTask(path string) (Task, error) {
	var task Task
	return task, readJSON(path, &task)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func listJSON(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func idsFromFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		base := f[:len(f)-len(filepath.Ext(f))]
		if i := len(base) - 32; i > 0 {
			out = append(out, base[i:])
		} else {
			out = append(out, base)
		}
	}
	return out
}

func filename(t time.Time, id string) string {
	return fmt.Sprintf("%020d-%s.json", t.UnixNano(), id)
}

func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func nonnil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
