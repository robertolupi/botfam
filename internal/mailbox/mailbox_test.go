package mailbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newMailbox(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "claude.mailbox")
}

func TestAppendAssignsOffsetSeqEpoch(t *testing.T) {
	path := newMailbox(t)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	a, err := w.Append(Event{Source: SourceIRC, Target: "#botfam", Nick: "agy", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != 0 {
		t.Errorf("first event id = %d, want 0", a.ID)
	}
	if a.Seq != 1 {
		t.Errorf("first irc seq = %d, want 1", a.Seq)
	}
	if a.Epoch != 1 {
		t.Errorf("epoch = %d, want 1", a.Epoch)
	}
	if a.TS.IsZero() {
		t.Error("timestamp not stamped")
	}

	b, err := w.Append(Event{Source: SourceForge, Repo: "botfam/botfam", Number: 230})
	if err != nil {
		t.Fatal(err)
	}
	// id of the second event must equal the byte length of the first line.
	fi, _ := os.Stat(path)
	if b.ID == a.ID {
		t.Error("second event reused first offset")
	}
	if b.Seq != 1 {
		t.Errorf("first forge seq = %d, want 1 (per-source counter)", b.Seq)
	}

	c, err := w.Append(Event{Source: SourceIRC, Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Seq != 2 {
		t.Errorf("second irc seq = %d, want 2", c.Seq)
	}
	if c.ID != fi.Size() {
		t.Errorf("third event id = %d, want %d (offset of its line)", c.ID, fi.Size())
	}
}

func TestReadFromRoundTrip(t *testing.T) {
	path := newMailbox(t)
	w, _ := OpenWriter(path)
	want := []string{"one", "two", "three"}
	for _, txt := range want {
		if _, err := w.Append(Event{Source: SourceIRC, Text: txt}); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	evs, next, err := ReadFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3", len(evs))
	}
	for i, ev := range evs {
		if ev.Text != want[i] {
			t.Errorf("event %d text = %q, want %q", i, ev.Text, want[i])
		}
		if ev.ID != evs[i].ID {
			t.Error("id mismatch")
		}
	}
	fi, _ := os.Stat(path)
	if next != fi.Size() {
		t.Errorf("next = %d, want EOF %d", next, fi.Size())
	}

	// Resume from a mid-file offset returns only later events.
	evs2, _, _ := ReadFrom(path, evs[1].ID)
	if len(evs2) != 2 || evs2[0].Text != "two" {
		t.Errorf("resume from second offset got %d events starting %q", len(evs2), text0(evs2))
	}
}

func TestReadFromNegativeIsEOF(t *testing.T) {
	path := newMailbox(t)
	w, _ := OpenWriter(path)
	w.Append(Event{Source: SourceIRC, Text: "stale"})
	w.Close()

	evs, next, err := ReadFrom(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("from<0 returned %d events, want 0 (EOF default)", len(evs))
	}
	fi, _ := os.Stat(path)
	if next != fi.Size() {
		t.Errorf("next = %d, want EOF %d", next, fi.Size())
	}
}

func TestReadFromLeavesPartialLine(t *testing.T) {
	path := newMailbox(t)
	w, _ := OpenWriter(path)
	first, _ := w.Append(Event{Source: SourceIRC, Text: "complete"})
	w.Close()

	// Append a partial line by hand (no trailing newline) — simulates a write
	// in progress.
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString(`{"source":"irc","text":"partial"`)
	f.Close()

	evs, next, err := ReadFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Text != "complete" {
		t.Fatalf("got %d events, want only the complete one", len(evs))
	}
	// next must point at the start of the partial line, not past it.
	wantNext := first.ID + lineLen(t, path, first.ID)
	if next != wantNext {
		t.Errorf("next = %d, want %d (before partial line)", next, wantNext)
	}
}

func TestReadFromSkipsMalformed(t *testing.T) {
	path := newMailbox(t)
	w, _ := OpenWriter(path)
	w.Append(Event{Source: SourceIRC, Text: "good"})
	w.Close()
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("this is not json\n")
	f.Close()
	w2, _ := OpenWriter(path)
	w2.Append(Event{Source: SourceIRC, Text: "after"})
	w2.Close()

	evs, _, err := ReadFrom(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2 (malformed skipped)", len(evs))
	}
	if evs[0].Text != "good" || evs[1].Text != "after" {
		t.Errorf("unexpected events: %q, %q", evs[0].Text, evs[1].Text)
	}
}

func TestCheckpointAndResumeSeq(t *testing.T) {
	path := newMailbox(t)
	w, _ := OpenWriter(path)
	w.Append(Event{Source: SourceIRC, Text: "a"})
	w.Append(Event{Source: SourceIRC, Text: "b"})
	w.Append(Event{Source: SourceForge, Number: 1})
	w.Checkpoint(Cursors{IRCLogOffset: 4096})
	// One more event after the checkpoint, so resume must count past the meta.
	w.Append(Event{Source: SourceForge, Number: 2})
	w.Close()

	// Reopen: seq must continue (irc=2, forge=2), cursors restored, epoch kept.
	w2, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if got := w2.Cursors(); got.IRCLogOffset != 4096 {
		t.Errorf("restored cursors = %+v, want IRCLogOffset 4096", got)
	}
	irc, _ := w2.Append(Event{Source: SourceIRC, Text: "c"})
	if irc.Seq != 3 {
		t.Errorf("resumed irc seq = %d, want 3", irc.Seq)
	}
	forge, _ := w2.Append(Event{Source: SourceForge, Number: 3})
	if forge.Seq != 3 {
		t.Errorf("resumed forge seq = %d, want 3 (counted event after checkpoint)", forge.Seq)
	}
}

func TestLastMeta(t *testing.T) {
	path := newMailbox(t)
	if _, ok, err := LastMeta(path); err != nil || ok {
		t.Errorf("LastMeta on missing file = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	w, _ := OpenWriter(path)
	w.Append(Event{Source: SourceIRC, Text: "x"})
	w.Checkpoint(Cursors{IRCLogOffset: 100})
	w.Append(Event{Source: SourceIRC, Text: "y"})
	w.Checkpoint(Cursors{IRCLogOffset: 200})
	w.Append(Event{Source: SourceIRC, Text: "z"})
	w.Close()

	meta, ok, err := LastMeta(path)
	if err != nil || !ok {
		t.Fatalf("LastMeta = (ok=%v, err=%v)", ok, err)
	}
	if meta.UpstreamCursors == nil || meta.UpstreamCursors.IRCLogOffset != 200 {
		t.Errorf("LastMeta returned %+v, want the second checkpoint (200)", meta.UpstreamCursors)
	}
}

func TestLastMetaBackwardScanMultiChunk(t *testing.T) {
	// Force the multi-chunk backward path with a tiny chunk size.
	defer func(orig int64) { metaScanChunk = orig }(metaScanChunk)
	metaScanChunk = 16

	path := newMailbox(t)
	w, _ := OpenWriter(path)
	w.Checkpoint(Cursors{IRCLogOffset: 1})
	// Many events (each longer than a chunk) after the meta, so the scan must
	// walk back across several chunks to find it.
	for i := 0; i < 50; i++ {
		w.Append(Event{Source: SourceIRC, Text: fmt.Sprintf("line-%02d-padded-out", i)})
	}
	w.Close()

	meta, ok, err := LastMeta(path)
	if err != nil || !ok {
		t.Fatalf("LastMeta = (ok=%v, err=%v)", ok, err)
	}
	if meta.Source != SourceMeta || meta.UpstreamCursors.IRCLogOffset != 1 {
		t.Errorf("multi-chunk LastMeta returned %+v", meta)
	}
}

func text0(evs []Event) string {
	if len(evs) == 0 {
		return ""
	}
	return evs[0].Text
}

// lineLen returns the on-disk byte length (including newline) of the line that
// starts at offset off.
func lineLen(t *testing.T, path string, off int64) int64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := off; i < int64(len(data)); i++ {
		if data[i] == '\n' {
			return i - off + 1
		}
	}
	return int64(len(data)) - off
}
