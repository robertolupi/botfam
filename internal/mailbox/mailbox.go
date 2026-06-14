// Package mailbox is the per-agent notification queue that backs `botfam wait`.
//
// A mailbox is an append-only JSONL file (one self-describing [Event] per line,
// discriminated by Source). A single writer — the ingest goroutine hosted in the
// botfam MCP server — appends IRC and forge events plus periodic "meta" cursor
// lines; any number of readers (`botfam wait`) seek into it by byte offset.
//
// Three invariants make the file safe to resume and rotate (proposal
// "Per-Agent Mailbox + Unified botfam wait", §3.1):
//
//   - id   = the byte offset of the line's first byte. It is the seek key, and
//     is only valid within its epoch.
//   - seq  = a monotonic per-source counter, global across rotations (carried
//     forward in the meta line, never reset), so a reader detects loss and
//     re-syncs by seq.
//   - epoch = the file generation. Rotation bumps it; a stale offset carried
//     across a rotation is detected (epoch mismatch), never misread.
package mailbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"
)

// Source values for [Event.Source].
const (
	SourceIRC   = "irc"
	SourceForge = "forge"
	SourceMeta  = "meta"
)

// Cursors are the upstream resume positions a writer checkpoints into a meta
// line so the ingester can resume tailing/polling after a restart without a
// separate state file.
type Cursors struct {
	IRCLogOffset            int64 `json:"irc_log_offset"`
	ForgeLastNotificationID int64 `json:"forge_last_notification_id"`
}

// Event is one mailbox line. It carries the common envelope (Source, Epoch, Seq,
// ID, TS) plus the fields for whichever Source it is; unused fields are omitted.
type Event struct {
	Source string    `json:"source"`
	Epoch  int64     `json:"epoch"`
	Seq    int64     `json:"seq,omitempty"`
	ID     int64     `json:"id"`
	TS     time.Time `json:"ts"`

	// IRC events.
	Target string `json:"target,omitempty"`
	Nick   string `json:"nick,omitempty"`
	Text   string `json:"text,omitempty"`

	// Forge events.
	Kind        string `json:"kind,omitempty"`
	SubjectType string `json:"subject_type,omitempty"`
	Repo        string `json:"repo,omitempty"`
	Number      int64  `json:"number,omitempty"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	NotifID     int64  `json:"notif_id,omitempty"`

	// Meta (cursor) events.
	UpstreamCursors *Cursors         `json:"upstream_cursors,omitempty"`
	Seqs            map[string]int64 `json:"seqs,omitempty"`
}

// metaScanChunk is the backward-read block size for [LastMeta]; a var so tests
// can shrink it to exercise the multi-chunk path.
var metaScanChunk = int64(64 * 1024)

// ReadFrom reads complete event lines from path starting at byte offset from.
// A negative from means "start at EOF" — it returns no events and next = size,
// which is how a fresh `wait` session avoids replaying stale backlog.
//
// It stops at EOF, leaving any trailing partial line (one still being written,
// no '\n' yet) unconsumed: next points just past the last complete line so the
// caller can resume cleanly. Malformed lines are skipped but still advance the
// offset. A missing file is reported as fs.ErrNotExist with next = from.
func ReadFrom(path string, from int64) (events []Event, next int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, from, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, from, err
	}
	size := fi.Size()
	if from < 0 || from > size {
		// from<0: start at EOF. from>size: the file shrank under us (truncation
		// or rotation) — the offset is stale, so resync at the current end and
		// let the caller notice the gap via seq/epoch.
		return nil, size, nil
	}

	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from, err
	}
	r := bufio.NewReader(f)
	next = from
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return events, next, rerr
		}
		// Only consume a complete line; a trailing fragment without '\n' is a
		// line still being written — leave next before it so it is reread whole.
		if strings.HasSuffix(line, "\n") {
			next += int64(len(line))
			if raw := strings.TrimSpace(line); raw != "" {
				var ev Event
				if json.Unmarshal([]byte(raw), &ev) == nil {
					events = append(events, ev)
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return events, next, nil
}

// LastMeta returns the most recent meta (cursor) line, scanning the file
// backward so a large mailbox is cheap to resume from. ok is false (with a nil
// error) when the file is absent or contains no meta line yet.
func LastMeta(path string) (ev *Event, ok bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, false, err
	}

	// head carries the leading partial line from the previously read (higher)
	// region, to be completed by the chunk that precedes it.
	var head []byte
	pos := fi.Size()
	for pos > 0 {
		n := metaScanChunk
		if pos < n {
			n = pos
		}
		pos -= n
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, pos); err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		buf = append(buf, head...)

		// The first line of buf is complete only once we've reached the start of
		// the file (pos == 0); otherwise carry it as head for the next chunk.
		complete := buf
		if pos > 0 {
			if nl := bytes.IndexByte(buf, '\n'); nl >= 0 {
				head = append([]byte(nil), buf[:nl]...)
				complete = buf[nl+1:]
			} else {
				head = append([]byte(nil), buf...)
				continue
			}
		}

		lines := bytes.Split(complete, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			raw := bytes.TrimSpace(lines[i])
			if len(raw) == 0 {
				continue
			}
			var e Event
			if json.Unmarshal(raw, &e) == nil && e.Source == SourceMeta {
				return &e, true, nil
			}
		}
	}
	return nil, false, nil
}
