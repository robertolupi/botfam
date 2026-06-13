package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionLifecycle(t *testing.T) {
	temp := t.TempDir()
	s := New(temp)

	// 1. Session New
	slug := "test-session"
	participants := []string{"alice", "bob"}
	if err := s.SessionNew(slug, participants, "operator", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	// 2. Duplicate Session New fails
	if err := s.SessionNew(slug, participants, "operator", "", nil, nil); err == nil {
		t.Fatal("expected error creating duplicate session")
	}

	// 3. Append entries
	lockAlice, err := s.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	entry1, err := s.SessionAppend(slug, "alice", "Hello this is Alice.", &SessionHandoff{
		Task:        "Review Alice's proposal",
		Context:     "doc/DESIGN_sessions.md",
		Deliverable: "ACK or critique",
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry1.Actor != "alice" || entry1.Body != "Hello this is Alice." {
		t.Fatalf("unexpected entry1: %+v", entry1)
	}
	_ = lockAlice.Close()

	time.Sleep(10 * time.Millisecond) // Ensure distinct timestamps

	lockBob, err := s.LockActor("bob")
	if err != nil {
		t.Fatal(err)
	}
	entry2, err := s.SessionAppend(slug, "bob", "Ack, reviewing now.", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = lockBob.Close()

	// 4. Read entries
	entries, err := s.SessionRead(slug, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != entry1.ID || entries[1].ID != entry2.ID {
		t.Fatalf("unexpected read entries: %+v", entries)
	}

	// Filter by actor
	aliceEntries, err := s.SessionRead(slug, "alice", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceEntries) != 1 || aliceEntries[0].ID != entry1.ID {
		t.Fatalf("unexpected filtered entries: %+v", aliceEntries)
	}

	// Filter by timestamp
	sinceEntries, err := s.SessionRead(slug, "", entry1.TS, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinceEntries) != 1 || sinceEntries[0].ID != entry2.ID {
		t.Fatalf("unexpected since filtered entries: %+v", sinceEntries)
	}

	// Filter by limit
	limitEntries, err := s.SessionRead(slug, "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limitEntries) != 1 || limitEntries[0].ID != entry2.ID {
		t.Fatalf("unexpected limit filtered entries: %+v", limitEntries)
	}

	// 5. List Sessions
	list, err := s.SessionList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != slug {
		t.Fatalf("expected 1 active session, got %+v", list)
	}

	// Write ARCHIVED tombstone
	archiveFile := filepath.Join(temp, "sessions", slug, "ARCHIVED")
	if err := os.WriteFile(archiveFile, []byte("archived"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session list should now be empty
	list, err = s.SessionList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 active sessions after archiving, got %+v", list)
	}

	// Clean up tombstone for next tests
	_ = os.Remove(archiveFile)

	// 6. Render
	rendered, err := s.SessionRender(slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "# Session: test-session") ||
		!strings.Contains(rendered, "## [alice,") ||
		!strings.Contains(rendered, "**→ Handoff:**") {
		t.Fatalf("rendered output incorrect:\n%s", rendered)
	}

	// 7. Close (write markdown to repo)
	repoRoot := filepath.Join(temp, "fake-repo")
	if err := s.SessionClose(slug, repoRoot); err != nil {
		t.Fatal(err)
	}
	sessionMdPath := filepath.Join(repoRoot, "wiki", "session-"+slug+".md")
	b, err := os.ReadFile(sessionMdPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != rendered {
		t.Fatalf("closed file does not match render output")
	}
}

func TestSessionTornLineTolerant(t *testing.T) {
	temp := t.TempDir()
	s := New(temp)

	slug := "torn-session"
	if err := s.SessionNew(slug, []string{"alice"}, "operator", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	lock, err := s.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	// Append valid entry
	entry, err := s.SessionAppend(slug, "alice", "Entry 1", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Force write a corrupt/torn JSON line manually
	jsonlPath := filepath.Join(temp, "sessions", slug, "session.jsonl")
	f, err := os.OpenFile(jsonlPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A line that is cut off (corrupt JSON)
	if _, err := f.WriteString("{\"id\":\"1234\", \"actor\":\"alice\",\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	// Append another valid entry
	entry2, err := s.SessionAppend(slug, "alice", "Entry 3", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read should skip the torn line and return the 2 valid entries
	entries, err := s.SessionRead(slug, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries (ignoring 1 torn), got %d: %+v", len(entries), entries)
	}
	if entries[0].ID != entry.ID || entries[1].ID != entry2.ID {
		t.Fatalf("returned incorrect entries: %+v", entries)
	}
}

func TestSessionAppendValidation(t *testing.T) {
	temp := t.TempDir()
	s := New(temp)

	slug := "val-session"
	if err := s.SessionNew(slug, []string{"alice"}, "operator", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	// Try appending without lock -> fails
	_, err := s.SessionAppend(slug, "alice", "No lock", nil)
	if err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Fatalf("expected error because actor is not locked, got: %v", err)
	}

	lock, err := s.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	// Try appending with invalid handoffs
	cases := []struct {
		name    string
		handoff *SessionHandoff
		wantErr string
	}{
		{
			name: "empty task",
			handoff: &SessionHandoff{
				Task:        "",
				Context:     "ctx",
				Deliverable: "deliv",
			},
			wantErr: "invalid handoff: task cannot be empty or whitespace only",
		},
		{
			name: "whitespace-only context",
			handoff: &SessionHandoff{
				Task:        "task",
				Context:     "   ",
				Deliverable: "deliv",
			},
			wantErr: "invalid handoff: context cannot be empty or whitespace only",
		},
		{
			name: "whitespace-only deliverable",
			handoff: &SessionHandoff{
				Task:        "task",
				Context:     "ctx",
				Deliverable: "\t\n",
			},
			wantErr: "invalid handoff: deliverable cannot be empty or whitespace only",
		},
		{
			name: "whitespace task",
			handoff: &SessionHandoff{
				Task:        "   ",
				Context:     "ctx",
				Deliverable: "deliv",
			},
			wantErr: "invalid handoff: task cannot be empty or whitespace only",
		},
		{
			name: "newline context",
			handoff: &SessionHandoff{
				Task:        "task",
				Context:     "\n",
				Deliverable: "deliv",
			},
			wantErr: "invalid handoff: context cannot be empty or whitespace only",
		},
		{
			name: "carriage return deliverable",
			handoff: &SessionHandoff{
				Task:        "task",
				Context:     "ctx",
				Deliverable: "\r",
			},
			wantErr: "invalid handoff: deliverable cannot be empty or whitespace only",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.SessionAppend(slug, "alice", "body", tc.handoff)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}
