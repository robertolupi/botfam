package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Maildir subdirectory names.
const (
	boxTmp = "tmp"
	boxNew = "new"
	boxCur = "cur"
)

// Spool is a per-agent Maildir at dir ($FAMROOT/spool/$agent). Delivery is
// lock-free via tmp/->new/ atomic rename; a read moves new/->cur/ (the ack).
// Multiple deliverers are safe concurrently (unique filenames + rename); the
// single-writer constraint of the ingester is a separate concern enforced above
// this layer.
type Spool struct {
	dir string

	// OnDeliver, if set, is invoked with each message immediately after it
	// becomes visible in new/ (post-rename). It is the hook the ingester uses to
	// fire the best-effort MCP notification nudge (#337). It must not block (the
	// caller may hold the writer lock) and must not mutate the spool.
	OnDeliver func(*Message)
}

// Open ensures the Maildir layout (tmp/new/cur) exists under dir and returns a
// handle. It is idempotent — opening an existing spool just revalidates the
// directories.
func Open(dir string) (*Spool, error) {
	for _, sub := range []string{boxTmp, boxNew, boxCur} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Spool{dir: dir}, nil
}

// Dir returns the spool's root directory.
func (s *Spool) Dir() string { return s.dir }

// deliveryCounter disambiguates two deliveries within the same nanosecond from
// the same process, keeping every filename unique and (with the time prefix)
// monotonically sortable in delivery order.
var deliveryCounter atomic.Uint64

// uniqueName builds a Maildir-style unique filename. The zero-padded nanosecond
// timestamp is the primary sort key (so files sort in delivery order across
// process restarts) and the zero-padded counter breaks within-nanosecond ties.
func uniqueName() string {
	n := deliveryCounter.Add(1)
	return fmt.Sprintf("%020d.%012d.%d.%s", time.Now().UnixNano(), n, os.Getpid(), fileHost())
}

// fileHost returns the hostname reduced to filename-safe characters (Maildir's
// third field), or "localhost" if it can't be determined.
func fileHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "localhost"
	}
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "localhost"
	}
	return b.String()
}

// Deliver writes m to tmp/ and atomically renames it into new/, returning the
// delivered filename. The rename is the durability/visibility point: a reader
// never sees a half-written message. On a rename failure the tmp file is cleaned
// up best-effort.
func (s *Spool) Deliver(m *Message) (string, error) {
	name := uniqueName()
	tmp := filepath.Join(s.dir, boxTmp, name)
	if err := os.WriteFile(tmp, m.Encode(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, boxNew, name)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if s.OnDeliver != nil {
		s.OnDeliver(m)
	}
	return name, nil
}

// Entry identifies a message file within a spool box (new/ or cur/).
type Entry struct {
	Name string
	box  string
	dir  string
}

// Path is the absolute path of the entry's file.
func (e Entry) Path() string { return filepath.Join(e.dir, e.box, e.Name) }

// ListNew returns the undelivered messages (new/) in delivery order.
func (s *Spool) ListNew() ([]Entry, error) { return s.list(boxNew) }

// ListCur returns the already-read messages (cur/, the replay buffer) in
// delivery order.
func (s *Spool) ListCur() ([]Entry, error) { return s.list(boxCur) }

func (s *Spool) list(box string) ([]Entry, error) {
	des, err := os.ReadDir(filepath.Join(s.dir, box))
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if de.IsDir() || strings.HasPrefix(name, ".") {
			continue // skip dotfiles (e.g. a partially-written tmp leak shouldn't be here anyway)
		}
		out = append(out, Entry{Name: name, box: box, dir: s.dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read parses the message file referenced by e.
func (s *Spool) Read(e Entry) (*Message, error) {
	b, err := os.ReadFile(e.Path())
	if err != nil {
		return nil, err
	}
	return ParseMessage(b)
}

// Ack marks a new/ message as read by moving it to cur/. It is the sole
// consumption mechanism (proposal §3): only this advances the delivery cursor.
func (s *Spool) Ack(e Entry) error {
	if e.box != boxNew {
		return fmt.Errorf("mailbox: Ack requires a new/ entry, got %q", e.box)
	}
	return os.Rename(e.Path(), filepath.Join(s.dir, boxCur, e.Name))
}
