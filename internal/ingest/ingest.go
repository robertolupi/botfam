package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/irc"
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

// ircPoller tails the IRC client log and appends matching lines as irc events.
type ircPoller struct {
	logPath   string
	matchNick string
}

// NewIRCPoller builds the IRC source: it tails logPath, skipping history
// replays and matchNick's own traffic (the same filter as irc-wait).
func NewIRCPoller(logPath, matchNick string) Poller {
	return &ircPoller{logPath: logPath, matchNick: matchNick}
}

func (p *ircPoller) Name() string { return mailbox.SourceIRC }

func (p *ircPoller) SeedEOF(c *mailbox.Cursors) {
	if fi, err := os.Stat(p.logPath); err == nil {
		c.IRCOffset = fi.Size()
	}
}

func (p *ircPoller) Poll(s *mailbox.Spool, c *mailbox.Cursors) error {
	lines, next, err := irc.ReadIrcLog(p.logPath, c.IRCOffset, 1000)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // log not created yet
		}
		return err
	}
	for _, line := range lines {
		if !irc.IsMatchingLine(line, p.matchNick) {
			continue
		}
		target, nick, text := parseIRCLine(line)
		// Subject is the line text (capped/sanitized by Message.Encode); the body
		// keeps the full raw line so nothing is lost on a parse miss.
		if _, err := s.Deliver(&mailbox.Message{
			Source:  mailbox.SourceIRC,
			From:    nick,
			To:      target,
			Kind:    "message",
			Subject: text,
			Body:    line,
		}); err != nil {
			return err
		}
	}
	c.IRCOffset = next
	return nil
}

// parseIRCLine does a light best-effort parse of a human-readable client log
// line into (target channel, nick, text). The full line is always kept as text
// so nothing is lost even when the parse misses.
func parseIRCLine(line string) (target, nick, text string) {
	text = line
	if i := strings.Index(line, "<"); i >= 0 {
		if j := strings.Index(line[i+1:], ">"); j >= 0 {
			nick = line[i+1 : i+1+j]
		}
	}
	for _, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, "#") {
			target = tok
			break
		}
	}
	return target, nick, text
}

// IngestParams resolves the spool directory, IRC log path, and fam-scoped match
// nick for the agent owning workDir, so a host can construct an Ingester. The
// spool lives at $FAMROOT/spool/$agent (proposal-event-delivery-redesign §3).
func IngestParams(workDir string) (spoolDir, ircLogPath, matchNick string, err error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return "", "", "", err
	}
	spoolDir = filepath.Join(rf.FamDir, "spool", rf.Actor)
	ircLogPath = filepath.Join(rf.WorktreeRoot, "scratch", "irc", rf.Actor, "log")
	matchNick = famconfig.FamScopedNick(rf.Actor, rf.Slug)
	return spoolDir, ircLogPath, matchNick, nil
}
