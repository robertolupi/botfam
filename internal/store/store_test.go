package store

import (
	"testing"
	"time"
)

func TestMessageReserveAckSeen(t *testing.T) {
	s := New(t.TempDir())
	msg, err := s.Send("alice", "bob", "handoff", map[string]any{"x": float64(1)}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != msg.ID {
		t.Fatalf("got %v, want message %s", got, msg.ID)
	}
	peek, err := s.Peek("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if peek != nil {
		t.Fatalf("peek saw reserved message: %v", peek)
	}
	if _, err := s.Ack("bob", msg.ID, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	seen, err := s.Seen("bob", msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("acked message was not seen")
	}
}

func TestRollbackProcessingRedelivers(t *testing.T) {
	s := New(t.TempDir())
	msg, err := s.Send("alice", "bob", "handoff", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TryRecv("bob", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RollbackProcessing("bob"); err != nil {
		t.Fatal(err)
	}
	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != msg.ID {
		t.Fatalf("redelivery got %v, want %s", got, msg.ID)
	}
}

func TestExpiredMessageIsNotDelivered(t *testing.T) {
	s := New(t.TempDir())
	expired := unixFloat(time.Now().Add(-time.Second))
	if _, err := s.Send("alice", "bob", "handoff", nil, "", &expired); err != nil {
		t.Fatal(err)
	}
	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expired message delivered: %v", got)
	}
	snap, err := s.Inbox("bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.New) != 0 {
		t.Fatalf("expired message remains in new: %v", snap.New)
	}
}

func TestTaskClaimComplete(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "task", map[string]any{"work": "x"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim("bob", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || claimed.Owner != "bob" {
		t.Fatalf("claim got %v, want task %s owned by bob", claimed, task.ID)
	}
	again, err := s.Claim("carol", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("second claim should not win: %v", again)
	}
	done, err := s.Complete("bob", task.ID, map[string]any{"done": true})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" {
		t.Fatalf("status = %s, want done", done.Status)
	}
}

func TestSweepExpiredLease(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("bob", -time.Second); err != nil {
		t.Fatal(err)
	}
	swept, err := s.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].ID != task.ID {
		t.Fatalf("swept = %v, want task %s", swept, task.ID)
	}
	claimed, err := s.Claim("carol", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID {
		t.Fatalf("reclaimed task got %v, want %s", claimed, task.ID)
	}
}
