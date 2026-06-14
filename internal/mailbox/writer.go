package mailbox

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Writer is the single appender to a mailbox file. It assigns each event its
// byte-offset id, per-source seq, and epoch, and persists its resume state in
// meta lines so it can recover statelessly via [OpenWriter]. The caller is
// responsible for the cross-process single-writer guarantee (an advisory flock
// on the mailbox; see the ingester). All methods are safe for concurrent use by
// the goroutines of one writer.
type Writer struct {
	mu      sync.Mutex
	f       *os.File
	epoch   int64
	offset  int64
	seqs    map[string]int64
	cursors Cursors
}

// OpenWriter opens path for appending, recovering epoch, per-source seqs, and
// upstream cursors from the last meta line plus any events appended after it.
// A fresh file starts at epoch 1 with empty cursors.
func OpenWriter(path string) (*Writer, error) {
	w := &Writer{seqs: map[string]int64{}, epoch: 1}

	if meta, ok, err := LastMeta(path); err != nil {
		return nil, err
	} else if ok {
		w.adopt(meta)
		// Account for events appended since that meta so seq does not regress.
		// ReadFrom starts at the meta line itself; adopt() re-applies it harmlessly.
		tail, _, err := ReadFrom(path, meta.ID)
		if err != nil {
			return nil, err
		}
		for i := range tail {
			ev := &tail[i]
			if ev.Source == SourceMeta {
				w.adopt(ev)
				continue
			}
			w.seqs[ev.Source]++
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	w.offset = fi.Size()
	w.f = f
	return w, nil
}

// adopt resets the writer's recovered state to a meta line's values.
func (w *Writer) adopt(meta *Event) {
	if meta.Epoch != 0 {
		w.epoch = meta.Epoch
	}
	w.seqs = map[string]int64{}
	for k, v := range meta.Seqs {
		w.seqs[k] = v
	}
	if meta.UpstreamCursors != nil {
		w.cursors = *meta.UpstreamCursors
	}
}

// writeLocked stamps ev with the current epoch, offset (as id), and a timestamp
// if unset, then appends it. The caller must hold w.mu.
func (w *Writer) writeLocked(ev *Event) error {
	ev.Epoch = w.epoch
	ev.ID = w.offset
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	n, err := w.f.Write(line)
	w.offset += int64(n)
	return err
}

// Append writes one source event (IRC or forge), assigning it the next per-source
// seq, and returns the stamped event (with its id/seq/epoch/ts filled in).
func (w *Writer) Append(ev Event) (Event, error) {
	if ev.Source == "" || ev.Source == SourceMeta {
		return ev, fmt.Errorf("mailbox: Append requires a source event, got %q", ev.Source)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seqs[ev.Source]++
	ev.Seq = w.seqs[ev.Source]
	if err := w.writeLocked(&ev); err != nil {
		return ev, err
	}
	return ev, nil
}

// Checkpoint appends a meta line carrying the given upstream cursors and the
// current per-source seqs, so a later [OpenWriter] (or a reader) can resume. It
// returns the stamped meta event.
func (w *Writer) Checkpoint(c Cursors) (Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cursors = c
	seqs := make(map[string]int64, len(w.seqs))
	for k, v := range w.seqs {
		seqs[k] = v
	}
	ev := Event{Source: SourceMeta, UpstreamCursors: &c, Seqs: seqs}
	if err := w.writeLocked(&ev); err != nil {
		return ev, err
	}
	return ev, nil
}

// Epoch returns the current file generation.
func (w *Writer) Epoch() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.epoch
}

// Offset returns the current end-of-file byte offset (the id the next appended
// event will get).
func (w *Writer) Offset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.offset
}

// Cursors returns the last checkpointed upstream cursors.
func (w *Writer) Cursors() Cursors {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursors
}

// Close closes the underlying file. The writer must not be used afterward.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
