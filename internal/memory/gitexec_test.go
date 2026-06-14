package memory

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// initBareWiki creates a bare repo with an initial commit on master, standing in
// for a <repo>.wiki.git remote. Returns the bare repo path (usable as a clone
// URL over the local filesystem).
func initBareWiki(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	bare := filepath.Join(root, "wiki.git")
	if out, err := runGit("", "init", "--bare", "-b", "master", bare); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	// Seed an initial commit so master exists and clones have a base.
	seed := filepath.Join(root, "seed")
	if out, err := runGit("", "clone", bare, seed); err != nil {
		t.Fatalf("clone seed: %v: %s", err, out)
	}
	if out, err := runGit(seed, "-c", "user.name=seed", "-c", "user.email=seed@x",
		"commit", "--allow-empty", "-m", "init wiki"); err != nil {
		t.Fatalf("seed commit: %v: %s", err, out)
	}
	if out, err := runGit(seed, "push", "origin", "HEAD:master"); err != nil {
		t.Fatalf("seed push: %v: %s", err, out)
	}
	return bare
}

func clone(t *testing.T, bare, dir string) *WikiGit {
	t.Helper()
	w, err := CloneWiki(bare, dir, "master")
	if err != nil {
		t.Fatalf("CloneWiki: %v", err)
	}
	return w
}

func TestWikiGitWriteRoundTrip(t *testing.T) {
	bare := initBareWiki(t)
	w := clone(t, bare, filepath.Join(t.TempDir(), "a"))
	s := NewStore(w)

	if err := s.Write(validMemory(), "claude"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// A fresh clone sees the published fact.
	w2 := clone(t, bare, filepath.Join(t.TempDir(), "b"))
	got, err := NewStore(w2).Load("Discovery resolution")
	if err != nil || got == nil {
		t.Fatalf("Load after publish = (%v, %v)", got, err)
	}
	if got.Body != "a fact" {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestWikiGitConcurrentDifferentFactRebases(t *testing.T) {
	bare := initBareWiki(t)
	a := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "a")))
	b := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "b")))

	// b publishes a different fact first.
	other := validMemory()
	other.Title = "IRC identity scoping"
	if err := b.Write(other, "agy"); err != nil {
		t.Fatalf("b.Write: %v", err)
	}
	// a's clone is now stale; writing a DIFFERENT fact must rebase + land, not
	// conflict.
	if err := a.Write(validMemory(), "claude"); err != nil {
		t.Fatalf("a.Write (different fact) = %v, want clean rebase+push", err)
	}
	// Both facts are present in a fresh clone.
	final := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "c")))
	for _, title := range []string{"Discovery resolution", "IRC identity scoping"} {
		m, err := final.Load(title)
		if err != nil || m == nil {
			t.Errorf("missing fact %q: (%v, %v)", title, m, err)
		}
	}
}

func TestWikiGitConcurrentSameFactConflicts(t *testing.T) {
	bare := initBareWiki(t)
	a := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "a")))
	b := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "b")))

	// Both write the SAME fact with different content; b lands first.
	bVer := validMemory()
	bVer.Body = "agy's version"
	if err := b.Write(bVer, "agy"); err != nil {
		t.Fatalf("b.Write: %v", err)
	}
	aVer := validMemory()
	aVer.Body = "claude's version"
	err := a.Write(aVer, "claude")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("a.Write (same fact) = %v, want ErrConflict", err)
	}
	// The conflict must NOT have clobbered b's landed version.
	final := NewStore(clone(t, bare, filepath.Join(t.TempDir(), "c")))
	m, err := final.Load("Discovery resolution")
	if err != nil || m == nil {
		t.Fatalf("Load = (%v, %v)", m, err)
	}
	if m.Body != "agy's version" {
		t.Errorf("Body = %q, want agy's version (no clobber)", m.Body)
	}
}

func TestWikiAuthURL(t *testing.T) {
	cases := []struct {
		base, owner, repo, token, want string
	}{
		{"http://gitea:3000/", "botfam", "botfam", "tok", "http://tok@gitea:3000/botfam/botfam.wiki.git"},
		{"http://gitea:3000", "botfam", "botfam", "", "http://gitea:3000/botfam/botfam.wiki.git"},
		{"https://example.com/", "o", "r", "t", "https://t@example.com/o/r.wiki.git"},
	}
	for _, c := range cases {
		if got := WikiAuthURL(c.base, c.owner, c.repo, c.token); got != c.want {
			t.Errorf("WikiAuthURL(%q,...) = %q, want %q", c.base, got, c.want)
		}
	}
}
