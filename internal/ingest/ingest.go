package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// Poller is one edge-triggered event source feeding a spool. Poll must deliver
// only events strictly past the cursor and advance the cursor in place.
type Poller interface {
	Name() string
	Poll(s *mailbox.Spool, c *mailbox.Cursors) error
}

// seeder is an optional Poller capability: on a brand-new spool (no prior
// cursor) SeedEOF positions the cursor at the source's current end so the first
// run does not dump the entire pre-existing backlog into the spool.
type seeder interface {
	SeedEOF(c *mailbox.Cursors)
}

// Ingester is the single writer that fills one agent's spool. It is meant to run
// as a background goroutine for the lifetime of its host (the botfam MCP server);
// it holds an advisory flock so that, across multiple harnesses of one agent,
// exactly one instance writes while the others stand by. (The flock guards the
// single-writer constraint, not delivery itself — delivery is lock-free via the
// spool's tmp/->new/ atomic rename.)
type Ingester struct {
	spoolDir string
	interval time.Duration
	pollers  []Poller

	// OnDeliver, if set, is wired to the spool so each delivered message fires
	// the best-effort MCP notification nudge (#337). Set before Run.
	OnDeliver func(*mailbox.Message)
}

// NewIngester builds an ingester for the spool at spoolDir that polls its sources
// every interval.
func NewIngester(spoolDir string, interval time.Duration, pollers ...Poller) *Ingester {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Ingester{
		spoolDir: spoolDir,
		interval: interval,
		pollers:  pollers,
	}
}

// Run opens the spool, acquires the writer lock (parking until it is free or ctx
// is done), does one synchronous poll immediately (so events that arrived while
// the host was down surface within milliseconds, not after a full poll interval),
// then polls on the interval until ctx is cancelled.
func (in *Ingester) Run(ctx context.Context) error {
	// Fail-fast (#263): the ingester writes the spool the reader watches, so a
	// drifted/garbage spoolDir must error loudly here rather than silently
	// fabricating a parallel spool tree the reader (on the famctx path) never
	// sees. The fam root is the spool's grandparent ($FAMROOT/spool/$agent ->
	// $FAMROOT); IngestParams resolves it from famctx, so its absence is a real
	// misconfiguration — refuse to MkdirAll a bogus tree under it.
	famRoot := filepath.Dir(filepath.Dir(in.spoolDir))
	if _, err := os.Stat(famRoot); err != nil {
		return fmt.Errorf("ingest: fam root %s does not exist; refusing to create spool at %s: %w", famRoot, in.spoolDir, err)
	}

	sp, err := mailbox.Open(in.spoolDir)
	if err != nil {
		return err
	}
	sp.OnDeliver = in.OnDeliver

	// The lock lives inside the (now-created) spool dir so the writer-lock
	// acquisition never races the spool-dir creation.
	lock, err := acquireWriterLock(ctx, filepath.Join(in.spoolDir, "lock"), in.interval)
	if err != nil {
		return err
	}
	defer lock.release()

	fresh := !sp.HasCursors()
	cur, err := sp.ReadCursors()
	if err != nil {
		return err
	}
	if fresh {
		// Fresh spool: start each source at its current end, like `wait`'s
		// default behavior, so we don't ingest the whole historical backlog.
		for _, p := range in.pollers {
			if s, ok := p.(seeder); ok {
				s.SeedEOF(&cur)
			}
		}
		if err := sp.WriteCursors(cur); err != nil {
			return err
		}
	}

	in.pollOnce(sp, &cur) // cold-start catch-up before the ticker

	t := time.NewTicker(in.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			in.pollOnce(sp, &cur)
		}
	}
}

// pollOnce runs every source once and checkpoints the cursors if any advanced.
// A poller error is skipped (best effort): one source failing must not stall the
// others or kill the ingester.
func (in *Ingester) pollOnce(sp *mailbox.Spool, cur *mailbox.Cursors) {
	before := *cur
	for _, p := range in.pollers {
		_ = p.Poll(sp, cur)
	}
	if *cur != before {
		_ = sp.WriteCursors(*cur)
	}
}

// writerLock is an advisory flock on the mailbox lock file. The kernel releases
// it when the fd closes (including on crash), so there is no stale-lock hazard.
type writerLock struct{ f *os.File }

func acquireWriterLock(ctx context.Context, path string, retry time.Duration) (*writerLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &writerLock{f}, nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(retry):
		}
	}
}

func (l *writerLock) release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}

// IngestParams resolves the spool directory for the agent owning workDir.
func IngestParams(workDir string) (spoolDir string, err error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return "", err
	}
	spoolDir = filepath.Join(rf.FamDir, "spool", rf.Actor)
	return spoolDir, nil
}
