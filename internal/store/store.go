package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

var (
	activeLocksMu sync.Mutex
	activeLocks   = make(map[string]bool)
)

func lockKey(root, actor string) string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	return filepath.Clean(filepath.Join(absRoot, actor, ".lock"))
}

type ActorLock struct {
	file *os.File
	key  string
}

func New(root string) Store {
	return &SqliteStore{Root: root}
}

func (l *ActorLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if l.key != "" {
		activeLocksMu.Lock()
		delete(activeLocks, l.key)
		activeLocksMu.Unlock()
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

func matchFilters(task Task, typeFilter, suggestedOwnerFilter string) bool {
	if typeFilter != "" && task.Type != typeFilter {
		return false
	}
	if suggestedOwnerFilter != "" {
		so, _ := task.Payload["suggested_owner"].(string)
		if so != suggestedOwnerFilter {
			return false
		}
	}
	return true
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
