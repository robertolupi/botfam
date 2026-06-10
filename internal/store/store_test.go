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
	claimed, err := s.Claim("bob", time.Minute, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || claimed.Owner != "bob" {
		t.Fatalf("claim got %v, want task %s owned by bob", claimed, task.ID)
	}
	again, err := s.Claim("carol", time.Minute, ClaimOptions{})
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
	if _, err := s.Claim("bob", -time.Second, ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	swept, err := s.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].ID != task.ID {
		t.Fatalf("swept = %v, want task %s", swept, task.ID)
	}
	claimed, err := s.Claim("carol", time.Minute, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID {
		t.Fatalf("reclaimed task got %v, want %s", claimed, task.ID)
	}
}

func TestClaimErgonomics(t *testing.T) {
	s := New(t.TempDir())

	// 1. Post tasks for testing filters
	task1, err := s.Post("alice", "typeA", map[string]any{"suggested_owner": "ownerX"})
	if err != nil {
		t.Fatal(err)
	}
	task2, err := s.Post("alice", "typeB", map[string]any{"suggested_owner": "ownerY"})
	if err != nil {
		t.Fatal(err)
	}

	// Filter no-match
	got, err := s.Claim("bob", time.Minute, ClaimOptions{Type: "typeC"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil claim for non-matching type filter, got %v", got)
	}

	got, err = s.Claim("bob", time.Minute, ClaimOptions{SuggestedOwner: "ownerZ"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil claim for non-matching suggested owner filter, got %v", got)
	}

	got, err = s.Claim("bob", time.Minute, ClaimOptions{Type: "typeA", SuggestedOwner: "ownerY"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil claim for mismatched type & suggested owner filters, got %v", got)
	}

	// Filter match
	got, err = s.Claim("bob", time.Minute, ClaimOptions{Type: "typeA"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != task1.ID {
		t.Fatalf("expected claim for task 1, got %v", got)
	}

	got, err = s.Claim("bob", time.Minute, ClaimOptions{SuggestedOwner: "ownerY"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != task2.ID {
		t.Fatalf("expected claim for task 2, got %v", got)
	}

	// 2. Claim-by-id hit
	task3, err := s.Post("alice", "typeA", map[string]any{"suggested_owner": "ownerX"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Claim("carol", time.Minute, ClaimOptions{TaskID: task3.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != task3.ID || got.Owner != "carol" {
		t.Fatalf("expected hit for task 3, got %v", got)
	}

	// 3. Claim-by-id with filter check
	task4, err := s.Post("alice", "typeB", map[string]any{"suggested_owner": "ownerY"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: task4.ID, Type: "typeB"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != task4.ID {
		t.Fatalf("expected claim for task 4 with matching filter, got %v", got)
	}

	task5, err := s.Post("alice", "typeC", map[string]any{"suggested_owner": "ownerZ"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: task5.ID, Type: "typeB"})
	if err == nil {
		t.Fatalf("expected filter mismatch error for task 5, got task %v", got)
	}

	got, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: task5.ID, SuggestedOwner: "ownerY"})
	if err == nil {
		t.Fatalf("expected suggested owner mismatch error for task 5, got task %v", got)
	}

	// 4. Claim-by-id miss
	// Absent task
	_, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: "absent-id"})
	if err == nil {
		t.Fatal("expected error for absent task ID")
	}

	// Already claimed
	_, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: task3.ID})
	if err == nil {
		t.Fatal("expected error for already claimed task ID")
	}

	// Completed
	_, err = s.Complete("carol", task3.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Claim("dan", time.Minute, ClaimOptions{TaskID: task3.ID})
	if err == nil {
		t.Fatal("expected error for completed task ID")
	}

	// 5. swept_from surfaced
	task6, err := s.Post("alice", "typeD", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Claim("eve", -time.Second, ClaimOptions{TaskID: task6.ID})
	if err != nil {
		t.Fatal(err)
	}
	swept, err := s.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].ID != task6.ID {
		t.Fatalf("expected 1 swept task, got %v", swept)
	}
	if swept[0].SweptFrom != "eve" {
		t.Fatalf("expected swept_from to be 'eve', got %q", swept[0].SweptFrom)
	}

	reclaimed, err := s.Claim("frank", time.Minute, ClaimOptions{TaskID: task6.ID})
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed == nil {
		t.Fatal("expected reclaimed task to be not nil")
	}
	if reclaimed.SweptFrom != "eve" {
		t.Fatalf("expected reclaimed task swept_from to be 'eve', got %q", reclaimed.SweptFrom)
	}
}
