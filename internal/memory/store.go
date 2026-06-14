package memory

import (
	"errors"
	"fmt"
)

// ErrConflict is returned when a concurrent writer changed the SAME fact (the
// same wiki page) between our read and our push. Per the compare-and-swap
// contract (proposal §4.8), this is a real disagreement to surface to the fam,
// never something to auto-resolve by force-push or blind retry.
var ErrConflict = errors.New("memory: concurrent write to the same fact; resolve on #botfam, do not force")

// ErrPushFailed is returned when the push keeps being rejected after a clean
// rebase — i.e. writers are racing faster than we can land. The caller retries.
var ErrPushFailed = errors.New("memory: push rejected repeatedly; retry")

// gitOps is the git seam behind the store so the compare-and-swap flow is
// testable without a live wiki repo. A real implementation drives `git` against
// a clone of <repo>.wiki.git (see gitexec.go); tests use a fake.
type gitOps interface {
	// ReadFile returns the current content of rel, or (nil, false, nil) if it
	// does not exist.
	ReadFile(rel string) (content []byte, exists bool, err error)
	// WriteCommit writes content to rel and commits it authored by author.
	WriteCommit(rel, content, author, message string) error
	// Push attempts to publish committed work. rejected is true when the remote
	// advanced (non-fast-forward); err is reserved for transport failures.
	Push() (rejected bool, err error)
	// PullRebase rebases local commits onto the advanced remote. It returns a
	// non-nil error when the rebase hits a conflict (i.e. the same file changed
	// on both sides); implementations must leave the working tree clean
	// (rebase aborted) in that case.
	PullRebase() error
}

// maxPushAttempts bounds the rebase+retry loop so a hot remote can't spin us
// forever; the caller surfaces ErrPushFailed and may retry at a higher level.
const maxPushAttempts = 5

// Store reads and writes shared-memory facts over a git-backed wiki repo. The
// wiki is the source of truth; the store owns format + the CAS write contract.
type Store struct {
	git gitOps
}

// NewStore wraps a gitOps backend.
func NewStore(git gitOps) *Store { return &Store{git: git} }

// Load returns the fact stored under title's slug, or (nil, nil) if absent.
func (s *Store) Load(title string) (*Memory, error) {
	slug := Slug(title)
	if slug == "" {
		return nil, fmt.Errorf("memory: empty slug for title %q", title)
	}
	content, exists, err := s.git.ReadFile(slug + ".md")
	if err != nil || !exists {
		return nil, err
	}
	m, err := Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("memory: parse %s: %w", slug, err)
	}
	return &m, nil
}

// Write creates or updates a fact, committing it authored by author and
// publishing it with compare-and-swap semantics: on a non-fast-forward push we
// rebase onto the advanced remote and retry, but a rebase conflict (a
// concurrent write to THIS fact) returns ErrConflict instead of clobbering.
func (s *Store) Write(m Memory, author string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	slug := Slug(m.Title)
	if slug == "" {
		return fmt.Errorf("memory: empty slug for title %q", m.Title)
	}
	rel := slug + ".md"
	msg := fmt.Sprintf("memory: write %s", slug)
	if err := s.git.WriteCommit(rel, m.Render(), author, msg); err != nil {
		return err
	}
	return s.publish(rel)
}

// Forget tombstones a fact (Status: Historical) rather than hard-deleting, so
// the audit trail survives (proposal Q5). It is a no-op-safe error if the fact
// is missing.
func (s *Store) Forget(title, author string) error {
	m, err := s.Load(title)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("memory: no fact titled %q to forget", title)
	}
	if m.Status == StatusHistorical {
		return nil
	}
	m.Status = StatusHistorical
	if !contains(m.Authors, author) {
		m.Authors = SortAuthors(append(m.Authors, author))
	}
	slug := Slug(title)
	rel := slug + ".md"
	if err := s.git.WriteCommit(rel, m.Render(), author, fmt.Sprintf("memory: forget %s", slug)); err != nil {
		return err
	}
	return s.publish(rel)
}

// publish runs the push / rebase-retry loop for a single committed file.
func (s *Store) publish(rel string) error {
	for attempt := 0; attempt < maxPushAttempts; attempt++ {
		rejected, err := s.git.Push()
		if err != nil {
			return err
		}
		if !rejected {
			return nil
		}
		// Remote advanced. Rebase onto it: a clean rebase means the other
		// writer touched a different fact (different file) — safe to retry.
		// A rebase error means the same file changed on both sides — a real
		// disagreement on this fact — surface it.
		if err := s.git.PullRebase(); err != nil {
			return fmt.Errorf("%w (%s): %v", ErrConflict, rel, err)
		}
	}
	return fmt.Errorf("%w (%s)", ErrPushFailed, rel)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
