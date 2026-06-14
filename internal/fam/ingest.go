package fam

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// Poller is one edge-triggered event source feeding a mailbox. Poll must append
// only events strictly past the cursor and advance the cursor in place.
type Poller interface {
	Name() string
	Poll(w *mailbox.Writer, c *mailbox.Cursors) error
}

// seeder is an optional Poller capability: on a brand-new mailbox (no prior
// cursor) SeedEOF positions the cursor at the source's current end so the first
// run does not dump the entire pre-existing backlog into the mailbox.
type seeder interface {
	SeedEOF(c *mailbox.Cursors)
}

// Ingester is the single writer that fills one agent's mailbox. It is meant to
// run as a background goroutine for the lifetime of its host (the botfam MCP
// server); it holds an advisory flock so that, across multiple harnesses of one
// agent, exactly one instance writes while the others stand by.
type Ingester struct {
	mailboxPath string
	lockPath    string
	interval    time.Duration
	pollers     []Poller
}

// NewIngester builds an ingester for mailboxPath that polls its sources every
// interval.
func NewIngester(mailboxPath string, interval time.Duration, pollers ...Poller) *Ingester {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Ingester{
		mailboxPath: mailboxPath,
		lockPath:    mailboxPath + ".lock",
		interval:    interval,
		pollers:     pollers,
	}
}

// Run acquires the writer lock (parking until it is free or ctx is done), opens
// the mailbox, does one synchronous poll immediately (so events that arrived
// while the host was down surface within milliseconds, not after a full poll
// interval), then polls on the interval until ctx is cancelled.
func (in *Ingester) Run(ctx context.Context) error {
	lock, err := acquireWriterLock(ctx, in.lockPath, in.interval)
	if err != nil {
		return err
	}
	defer lock.release()

	_, hadMeta, err := mailbox.LastMeta(in.mailboxPath)
	if err != nil {
		return err
	}

	w, err := mailbox.OpenWriter(in.mailboxPath)
	if err != nil {
		return err
	}
	defer w.Close()

	cur := w.Cursors()
	if !hadMeta {
		// Fresh mailbox: start each source at its current end, like `wait`'s
		// default-EOF, so we don't ingest the whole historical backlog.
		for _, p := range in.pollers {
			if s, ok := p.(seeder); ok {
				s.SeedEOF(&cur)
			}
		}
		if _, err := w.Checkpoint(cur); err != nil {
			return err
		}
	}

	in.pollOnce(w, &cur) // cold-start catch-up before the ticker

	t := time.NewTicker(in.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			in.pollOnce(w, &cur)
		}
	}
}

// pollOnce runs every source once and checkpoints the cursors if any advanced.
// A poller error is skipped (best effort): one source failing must not stall the
// others or kill the ingester.
func (in *Ingester) pollOnce(w *mailbox.Writer, cur *mailbox.Cursors) {
	before := *cur
	for _, p := range in.pollers {
		_ = p.Poll(w, cur)
	}
	if *cur != before {
		_, _ = w.Checkpoint(*cur)
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
		c.IRCLogOffset = fi.Size()
	}
}

func (p *ircPoller) Poll(w *mailbox.Writer, c *mailbox.Cursors) error {
	lines, next, err := ReadIrcLog(p.logPath, c.IRCLogOffset, 1000)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // log not created yet
		}
		return err
	}
	for _, line := range lines {
		if !isMatchingLine(line, p.matchNick) {
			continue
		}
		target, nick, text := parseIRCLine(line)
		if _, err := w.Append(mailbox.Event{
			Source: mailbox.SourceIRC, Target: target, Nick: nick, Text: text,
		}); err != nil {
			return err
		}
	}
	c.IRCLogOffset = next
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

// IngestParams resolves the mailbox path, IRC log path, and fam-scoped match
// nick for the agent owning workDir, so a host can construct an Ingester.
func IngestParams(workDir string) (mailboxPath, ircLogPath, matchNick string, err error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return "", "", "", err
	}
	mailboxPath = filepath.Join(rf.FamDir, rf.Actor+".mailbox")
	ircLogPath = filepath.Join(rf.WorktreeRoot, "scratch", "irc", rf.Actor, "log")
	matchNick = FamScopedNick(rf.Actor, rf.Slug)
	return mailboxPath, ircLogPath, matchNick, nil
}
