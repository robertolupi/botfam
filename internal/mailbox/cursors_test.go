package mailbox

import (
	"path/filepath"
	"testing"
)

func TestCursorsMissingIsZero(t *testing.T) {
	sp, _ := Open(filepath.Join(t.TempDir(), "claude"))
	if sp.HasCursors() {
		t.Error("fresh spool reports HasCursors = true")
	}
	c, err := sp.ReadCursors()
	if err != nil {
		t.Fatal(err)
	}
	if c != (Cursors{}) {
		t.Errorf("missing cursors = %+v, want zero", c)
	}
}

func TestCursorsRoundTrip(t *testing.T) {
	sp, _ := Open(filepath.Join(t.TempDir(), "claude"))
	want := Cursors{IRCOffset: 4096, ForgeWatermark: 12345}
	if err := sp.WriteCursors(want); err != nil {
		t.Fatal(err)
	}
	if !sp.HasCursors() {
		t.Error("HasCursors = false after WriteCursors")
	}
	got, err := sp.ReadCursors()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("cursors = %+v, want %+v", got, want)
	}
}
