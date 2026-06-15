package mailbox

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// cursorsFile is the upstream-resume pointer file at the spool root. It is the
// one small piece of resume bookkeeping outside the message files (proposal §3),
// chosen over a meta-message for simplicity. It is a resume pointer, not a
// decision plane.
const cursorsFile = "cursors.json"

// Cursors are the upstream positions the ingester checkpoints so it can resume
// tailing after a restart without re-dumping or skipping events. IRCOffset is
// the byte offset into the IRC client log; ForgeWatermark is the high-water
// notification **updated_at** as a Unix timestamp (seconds). It is a timestamp,
// not a notification id, because Gitea reuses a thread's notification id across
// activity and only bumps updated_at — so an id watermark silently skips an
// updated thread (new comment/assignee/close), while an updated_at watermark
// re-surfaces it (#169). A value below ~year-2001 (including 0, fresh, and any
// legacy id watermark) is treated as unset and re-seeded.
type Cursors struct {
	IRCOffset      int64 `json:"irc_offset"`
	ForgeWatermark int64 `json:"forge_watermark"`
}

// HasCursors reports whether a cursors.json exists yet. The ingester uses its
// absence to mean "fresh spool" (seed each source to its current end so the
// first run doesn't ingest the whole historical backlog).
func (s *Spool) HasCursors() bool {
	_, err := os.Stat(filepath.Join(s.dir, cursorsFile))
	return err == nil
}

// ReadCursors loads the resume pointers. A missing file yields the zero Cursors
// with a nil error (a fresh spool resumes from nothing).
func (s *Spool) ReadCursors() (Cursors, error) {
	var c Cursors
	b, err := os.ReadFile(filepath.Join(s.dir, cursorsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// WriteCursors persists the resume pointers atomically: it writes to a tmp/ file
// (same filesystem as the destination) and renames it over cursors.json, so a
// reader never observes a half-written pointer.
func (s *Spool) WriteCursors(c Cursors) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, boxTmp, "cursors."+uniqueName())
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, cursorsFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
