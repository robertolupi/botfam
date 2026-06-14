package memory

import (
	"errors"
	"testing"
)

// fakeGit simulates a wiki-git backend for the compare-and-swap flow. It
// scripts a sequence of push outcomes and records whether a rebase conflict
// should be reported, so the store's CAS branches can be exercised without git.
type fakeGit struct {
	files map[string]string

	pushRejectN int  // reject the first N pushes (remote advanced)
	rebaseFails bool // PullRebase reports a same-file conflict

	writes      int
	pushes      int
	rebases     int
	lastContent string
}

func (f *fakeGit) ReadFile(rel string) ([]byte, bool, error) {
	c, ok := f.files[rel]
	if !ok {
		return nil, false, nil
	}
	return []byte(c), true, nil
}

func (f *fakeGit) WriteCommit(rel, content, author, message string) error {
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[rel] = content
	f.lastContent = content
	f.writes++
	return nil
}

func (f *fakeGit) Push() (bool, error) {
	f.pushes++
	if f.pushRejectN > 0 {
		f.pushRejectN--
		return true, nil
	}
	return false, nil
}

func (f *fakeGit) PullRebase() error {
	f.rebases++
	if f.rebaseFails {
		return errors.New("rebase conflict in memory-x.md")
	}
	return nil
}

func validMemory() Memory {
	return Memory{
		Title:   "Discovery resolution",
		Authors: []string{"claude"},
		Created: "2026-06-14",
		Scope:   ScopeFam,
		Type:    TypeProject,
		Body:    "a fact",
	}
}

func TestWriteCleanPush(t *testing.T) {
	g := &fakeGit{}
	s := NewStore(g)
	if err := s.Write(validMemory(), "claude"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if g.writes != 1 || g.pushes != 1 || g.rebases != 0 {
		t.Errorf("writes=%d pushes=%d rebases=%d, want 1/1/0", g.writes, g.pushes, g.rebases)
	}
	if _, ok := g.files["memory-discovery-resolution.md"]; !ok {
		t.Errorf("file not written under expected slug: %v", g.files)
	}
}

func TestWriteRebasesOnDifferentFact(t *testing.T) {
	// Remote advanced once with a different fact: non-ff push, clean rebase,
	// retry succeeds.
	g := &fakeGit{pushRejectN: 1, rebaseFails: false}
	s := NewStore(g)
	if err := s.Write(validMemory(), "claude"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if g.pushes != 2 || g.rebases != 1 {
		t.Errorf("pushes=%d rebases=%d, want 2/1", g.pushes, g.rebases)
	}
}

func TestWriteConflictOnSameFact(t *testing.T) {
	// Remote advanced AND the rebase conflicts (same fact changed): surface,
	// never clobber.
	g := &fakeGit{pushRejectN: 1, rebaseFails: true}
	s := NewStore(g)
	err := s.Write(validMemory(), "claude")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Write err = %v, want ErrConflict", err)
	}
}

func TestWriteGivesUpAfterMaxAttempts(t *testing.T) {
	// Remote keeps advancing on other facts forever: clean rebases, but pushes
	// never land — bounded, returns ErrPushFailed.
	g := &fakeGit{pushRejectN: 999, rebaseFails: false}
	s := NewStore(g)
	err := s.Write(validMemory(), "claude")
	if !errors.Is(err, ErrPushFailed) {
		t.Fatalf("Write err = %v, want ErrPushFailed", err)
	}
	if g.pushes != maxPushAttempts {
		t.Errorf("pushes=%d, want %d", g.pushes, maxPushAttempts)
	}
}

func TestWriteRejectsInvalid(t *testing.T) {
	g := &fakeGit{}
	s := NewStore(g)
	if err := s.Write(Memory{Title: "t"}, "claude"); err == nil {
		t.Error("expected validation error for incomplete memory")
	}
	if g.writes != 0 {
		t.Error("invalid memory must not be committed")
	}
}

func TestForgetTombstones(t *testing.T) {
	g := &fakeGit{}
	s := NewStore(g)
	if err := s.Write(validMemory(), "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("Discovery resolution", "agy"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	got, err := s.Load("Discovery resolution")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusHistorical {
		t.Errorf("Status = %q, want Historical", got.Status)
	}
	if !contains(got.Authors, "agy") {
		t.Errorf("forgetting author not recorded: %v", got.Authors)
	}
}

func TestForgetMissing(t *testing.T) {
	g := &fakeGit{}
	s := NewStore(g)
	if err := s.Forget("nope", "claude"); err == nil {
		t.Error("expected error forgetting a missing fact")
	}
}

func TestLoadAbsent(t *testing.T) {
	g := &fakeGit{}
	s := NewStore(g)
	m, err := s.Load("nothing here")
	if err != nil || m != nil {
		t.Errorf("Load absent = (%v, %v), want (nil, nil)", m, err)
	}
}
