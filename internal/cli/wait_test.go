package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// failWriter fails every write, modelling a broken stdout / errored redirect.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func seedSpool(t *testing.T) (dir string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{
		Source: mailbox.SourceIRC, From: "agy", To: "#botfam", Subject: "claude: ping", Body: "claude: ping",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{
		Source: mailbox.SourceForge, Subject: `issue: botfam/botfam#230 "feat: x"`, Body: "http://gitea:3000/botfam/botfam/issues/230",
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// countMessages reports how many "===== message N/M =====" banners are in out.
func countMessages(out string) int {
	return strings.Count(out, "===== message ")
}

func TestWaitDrainsExistingEvents(t *testing.T) {
	dir := seedSpool(t)
	var out bytes.Buffer
	if err := runWait(context.Background(), &out, io.Discard, dir, parseSources("irc,forge"), false, 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if n := countMessages(s); n != 2 {
		t.Fatalf("got %d message banners, want 2 (drain-both):\n%s", n, s)
	}
	// irc was delivered first, so it is message 1, and it wakes us.
	if !strings.Contains(s, "===== message 1/2 · irc =====") {
		t.Errorf("first message banner not irc:\n%s", s)
	}
	// Verbatim body present (no JSON escaping).
	if !strings.Contains(s, "claude: ping") || !strings.Contains(s, "issue: botfam/botfam#230") {
		t.Errorf("verbatim message content missing:\n%s", s)
	}
	if !strings.Contains(s, "===== woke: 2 messages =====") {
		t.Errorf("woke footer missing/wrong:\n%s", s)
	}
}

// TestWaitDND: with do-not-disturb (the default), a non-directed forge event is
// drained but does not wake; an IRC line always does. A directed forge event
// wakes. --all (directedOnly=false) surfaces everything.
func TestWaitDND(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A non-directed forge event + an IRC line.
	if _, err := sp.Deliver(&mailbox.Message{Source: mailbox.SourceForge, Subject: "issue: x#1", Body: "u", Directed: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{Source: mailbox.SourceIRC, From: "agy", To: "#botfam", Subject: "hi", Body: "hi"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	// DND on: only the IRC line surfaces; the non-directed forge event is acked
	// (drained) but not shown.
	if err := runWait(context.Background(), &out, io.Discard, dir, parseSources("irc,forge"), true, 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if countMessages(s) != 1 || !strings.Contains(s, "· irc =====") {
		t.Fatalf("DND should surface only the IRC line, got:\n%s", s)
	}
	if strings.Contains(s, "· forge =====") {
		t.Errorf("DND surfaced a non-directed forge event:\n%s", s)
	}

	// A directed forge event now wakes even under DND.
	if _, err := sp.Deliver(&mailbox.Message{Source: mailbox.SourceForge, Subject: "issue: x#2", Body: "u", Directed: true}); err != nil {
		t.Fatal(err)
	}
	var out2 bytes.Buffer
	if err := runWait(context.Background(), &out2, io.Discard, dir, parseSources("irc,forge"), true, 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2.String(), "· forge =====") {
		t.Errorf("DND should surface a directed forge event:\n%s", out2.String())
	}
}

func TestWaitSourceFilter(t *testing.T) {
	dir := seedSpool(t)
	var out bytes.Buffer
	if err := runWait(context.Background(), &out, io.Discard, dir, parseSources("forge"), false, 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if n := countMessages(s); n != 1 {
		t.Fatalf("source filter surfaced %d messages, want only forge:\n%s", n, s)
	}
	if !strings.Contains(s, "· forge =====") {
		t.Errorf("surfaced message is not forge:\n%s", s)
	}
	// The filtered-out irc message must not be emitted.
	if strings.Contains(s, "claude: ping") {
		t.Errorf("filtered-out irc message leaked into output:\n%s", s)
	}
	if !strings.Contains(s, "===== woke: 1 message =====") {
		t.Errorf("woke footer missing/wrong:\n%s", s)
	}
}

// TestWaitAcksDrainedMessages: a read moves new/->cur/ (the ack), so a second
// wait with nothing new must time out rather than re-surface the batch — and the
// filtered-out source is acked too (consumed), not left to re-appear.
func TestWaitAcksDrainedMessages(t *testing.T) {
	dir := seedSpool(t)
	var first bytes.Buffer
	if err := runWait(context.Background(), &first, io.Discard, dir, parseSources("forge"), false, 2*time.Second, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if n := countMessages(first.String()); n != 1 {
		t.Fatalf("first drain surfaced %d, want 1", n)
	}

	var second bytes.Buffer
	if err := runWait(context.Background(), &second, io.Discard, dir, parseSources("irc,forge"), false, 150*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := second.String()
	if n := countMessages(s); n != 0 {
		t.Fatalf("re-drain surfaced %d already-acked messages, want 0:\n%s", n, s)
	}
	if !strings.Contains(s, "===== timed out =====") {
		t.Errorf("expected timed-out footer:\n%s", s)
	}
}

// TestWaitDoesNotAckOnWriteError: if surfaced output fails to write, the wake
// payload must NOT be acked — it stays in new/ for the next wait to re-deliver
// (at-least-once: a dup beats a silently-dropped wake). Regression for the
// codex review finding on #345.
func TestWaitDoesNotAckOnWriteError(t *testing.T) {
	dir := seedSpool(t)
	err := runWait(context.Background(), failWriter{}, io.Discard, dir, parseSources("irc,forge"), false, 2*time.Second, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when surfaced output fails to write")
	}
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if news, _ := sp.ListNew(); len(news) != 2 {
		t.Fatalf("messages were consumed despite the write failure: new/ has %d, want 2", len(news))
	}
	if curs, _ := sp.ListCur(); len(curs) != 0 {
		t.Errorf("cur/ has %d, want 0 (nothing should be acked on write failure)", len(curs))
	}
}

func TestWaitBlocksThenWakes(t *testing.T) {
	dir := seedSpool(t)
	// Drain the seeded backlog so new/ is empty before we block.
	if err := runWait(context.Background(), io.Discard, io.Discard, dir, parseSources("irc,forge"), false, 2*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		_ = runWait(context.Background(), &out, io.Discard, dir, parseSources("irc,forge"), false, 3*time.Second, 10*time.Millisecond)
		done <- out.String()
	}()

	time.Sleep(50 * time.Millisecond) // let the waiter start blocking
	sp, err := mailbox.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Deliver(&mailbox.Message{Source: mailbox.SourceForge, Subject: "woke you", Body: "woke you"}); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-done:
		if countMessages(s) != 1 || !strings.Contains(s, "woke you") {
			t.Fatalf("woke with wrong output:\n%s", s)
		}
		if strings.Contains(s, "timed out") {
			t.Errorf("reported timeout despite a delivery:\n%s", s)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("wait did not wake on delivery")
	}
}

// TestWaitContextCancel: cancelling the context unblocks an idle wait promptly
// (no infinite silent block — #276).
func TestWaitContextCancel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if _, err := mailbox.Open(dir); err != nil { // empty spool, nothing to drain
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWait(ctx, io.Discard, io.Discard, dir, parseSources("irc,forge"), false, 0 /* block forever */, 10*time.Millisecond)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected a non-nil error (context cancelled), got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not unblock on context cancel")
	}
}

// TestWaitFailFastMissingSpool: a missing spool errors immediately with the
// resolved absolute path, rather than blocking forever (#263).
func TestWaitFailFastMissingSpool(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "spool")
	err := runWait(context.Background(), io.Discard, io.Discard, missing, parseSources("irc,forge"), false, 0, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected a fail-fast error for a missing spool, got nil")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error should name the resolved absolute path %q, got: %v", missing, err)
	}
}

func TestReplayFromCur(t *testing.T) {
	dir := seedSpool(t)
	// Drain to move the seeded messages into cur/ (the replay buffer).
	if err := runWait(context.Background(), io.Discard, io.Discard, dir, parseSources("irc,forge"), false, 2*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runReplay(&out, io.Discard, dir, parseSources("irc,forge"), 0 /* all */); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if n := countMessages(s); n != 2 {
		t.Fatalf("replay surfaced %d messages, want 2:\n%s", n, s)
	}
	if !strings.Contains(s, "claude: ping") || !strings.Contains(s, "issue: botfam/botfam#230") {
		t.Errorf("replay missing verbatim content:\n%s", s)
	}
	if !strings.Contains(s, "===== replayed: 2 messages =====") {
		t.Errorf("replay footer missing/wrong:\n%s", s)
	}

	// Replay does not ack: new/ stays empty, cur/ still holds them, and a second
	// replay returns the same set.
	var again bytes.Buffer
	if err := runReplay(&again, io.Discard, dir, parseSources("irc,forge"), 0); err != nil {
		t.Fatal(err)
	}
	if countMessages(again.String()) != 2 {
		t.Errorf("replay is non-destructive: second replay should still show 2:\n%s", again.String())
	}
}

// fakeTimeline serves canned timelines per GetIssueTimeline call, advancing
// through the list so the watcher sees the set grow.
type fakeTimeline struct {
	calls   int
	perCall [][]*forge.TimelineEvent
}

func (f *fakeTimeline) GetIssueTimeline(int) ([]*forge.TimelineEvent, error) {
	i := f.calls
	if i >= len(f.perCall) {
		i = len(f.perCall) - 1
	}
	f.calls++
	return f.perCall[i], nil
}

func ev(id int64, typ, user, body string) *forge.TimelineEvent {
	e := &forge.TimelineEvent{ID: id, Type: typ, Body: body}
	if user != "" {
		e.User = &struct {
			Login string `json:"login"`
		}{Login: user}
	}
	return e
}

func TestWatchItemWakesOnNewEvent(t *testing.T) {
	// Baseline has event 1; a later poll adds event 2 (a close) → wake on it.
	tc := &fakeTimeline{perCall: [][]*forge.TimelineEvent{
		{ev(1, "comment", "rlupi", "first")},                              // baseline
		{ev(1, "comment", "rlupi", "first"), ev(2, "close", "rlupi", "")}, // new event appears
	}}
	var out bytes.Buffer
	if err := runWatchItem(context.Background(), &out, io.Discard, tc, "botfam/botfam", 123, 2*time.Second, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "event 1/1 · botfam/botfam#123") || !strings.Contains(s, "close by rlupi") {
		t.Fatalf("did not surface the new close event:\n%s", s)
	}
	if strings.Contains(s, "first") {
		t.Errorf("re-surfaced the baseline event:\n%s", s)
	}
	if !strings.Contains(s, "===== woke: 1 message on botfam/botfam#123 =====") {
		t.Errorf("missing woke footer:\n%s", s)
	}
}

func TestWatchItemTimesOut(t *testing.T) {
	// Timeline never changes → time out, no false wake.
	tc := &fakeTimeline{perCall: [][]*forge.TimelineEvent{{ev(1, "comment", "rlupi", "x")}}}
	var out bytes.Buffer
	if err := runWatchItem(context.Background(), &out, io.Discard, tc, "botfam/botfam", 123, 60*time.Millisecond, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "===== timed out =====") {
		t.Errorf("expected timeout, got:\n%s", out.String())
	}
}
