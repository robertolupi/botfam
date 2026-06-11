package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestMessageLifecycleSendRecvAckSeen(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"key": "value"}
	msg, err := s.Send("alice", "bob", "handoff", payload, "reply123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.From != "alice" || msg.To != "bob" || msg.Type != "handoff" || msg.InReplyTo != "reply123" {
		t.Fatalf("unexpected sent message fields: %+v", msg)
	}
	if msg.Payload["key"] != "value" {
		t.Fatalf("expected payload key=value, got: %+v", msg.Payload)
	}

	// Verify it is in the new folder
	newFiles, err := listJSON(filepath.Join(s.RootPath(), "bob", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if len(newFiles) != 1 {
		t.Fatalf("expected 1 new message, got: %d", len(newFiles))
	}

	// Peek should see it
	peeked, err := s.Peek("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if peeked == nil || peeked.ID != msg.ID {
		t.Fatalf("expected to peek message %s, got: %v", msg.ID, peeked)
	}

	// Receive it
	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != msg.ID {
		t.Fatalf("expected to receive message %s, got: %v", msg.ID, got)
	}

	// Verify processing
	procFiles, err := listJSON(filepath.Join(s.RootPath(), "bob", "processing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(procFiles) != 1 {
		t.Fatalf("expected 1 processing message, got: %d", len(procFiles))
	}

	// Peek should no longer see it
	peeked, err = s.Peek("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if peeked != nil {
		t.Fatalf("peek saw reserved message: %v", peeked)
	}

	// Seen should be false
	seen, err := s.Seen("bob", msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("message marked seen before ack")
	}

	// Ack it
	outcome := map[string]any{"success": true}
	acked, err := s.Ack("bob", msg.ID, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if acked == nil || acked.ID != msg.ID {
		t.Fatalf("expected to ack message %s, got: %v", msg.ID, acked)
	}

	// Verify cur
	curFiles, err := listJSON(filepath.Join(s.RootPath(), "bob", "cur"))
	if err != nil {
		t.Fatal(err)
	}
	if len(curFiles) != 1 {
		t.Fatalf("expected 1 cur message, got: %d", len(curFiles))
	}

	// Seen should be true
	seen, err = s.Seen("bob", msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("expected message to be seen after ack")
	}
}

func TestMessageCrashRedelivery(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	msg, err := s.Send("alice", "bob", "handoff", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != msg.ID {
		t.Fatalf("expected to reserve message, got: %v", got)
	}

	procFiles, _ := listJSON(filepath.Join(s.RootPath(), "bob", "processing"))
	if len(procFiles) != 1 {
		t.Fatalf("expected message to be in processing, got %d files", len(procFiles))
	}

	if err := s.RollbackProcessing("bob"); err != nil {
		t.Fatal(err)
	}

	procFiles, _ = listJSON(filepath.Join(s.RootPath(), "bob", "processing"))
	if len(procFiles) != 0 {
		t.Fatalf("expected processing to be empty, got %d files", len(procFiles))
	}
	newFiles, _ := listJSON(filepath.Join(s.RootPath(), "bob", "new"))
	if len(newFiles) != 1 {
		t.Fatalf("expected message to return to new, got %d files", len(newFiles))
	}

	got2, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got2 == nil || got2.ID != msg.ID {
		t.Fatalf("expected redelivery, got: %v", got2)
	}
}

func TestMessageSeenDedup(t *testing.T) {
	s := New(t.TempDir())
	msg, err := s.Send("alice", "bob", "handoff", nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	seen, err := s.Seen("bob", msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("expected Seen to return false for unsent/un-acked message")
	}

	seenNone, err := s.Seen("bob", "nonexistent-id")
	if err != nil {
		t.Fatal(err)
	}
	if seenNone {
		t.Fatal("expected Seen to return false for nonexistent message")
	}

	_, err = s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Ack("bob", msg.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	seenAfter, err := s.Seen("bob", msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !seenAfter {
		t.Fatal("expected Seen to return true after Ack")
	}
}

func TestMessageExpiryDeadLetter(t *testing.T) {
	s := New(t.TempDir())
	pastTime := unixFloat(time.Now().Add(-time.Hour))
	_, err := s.Send("alice", "bob", "handoff", nil, "", &pastTime)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.TryRecv("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("received expired message: %v", got)
	}

	expiredFiles, err := listJSON(filepath.Join(s.RootPath(), "bob", "expired"))
	if err != nil {
		t.Fatal(err)
	}
	if len(expiredFiles) != 1 {
		t.Fatalf("expected 1 file in expired, got: %d", len(expiredFiles))
	}

	newFiles, _ := listJSON(filepath.Join(s.RootPath(), "bob", "new"))
	if len(newFiles) != 0 {
		t.Fatalf("expected 0 files in new, got: %d", len(newFiles))
	}
}

func TestTaskLifecyclePostClaimHeartbeatComplete(t *testing.T) {
	s := New(t.TempDir())
	payload := map[string]any{"taskData": "run"}
	task, err := s.Post("alice", "test-task", payload)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "open" || task.Type != "test-task" || task.Payload["taskData"] != "run" {
		t.Fatalf("unexpected task state: %+v", task)
	}

	openFiles, _ := listJSON(filepath.Join(s.RootPath(), "tasks", "open"))
	if len(openFiles) != 1 {
		t.Fatalf("expected 1 open task file, got: %d", len(openFiles))
	}

	claimed, err := s.Claim("bob", time.Minute, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || claimed.Status != "claimed" || claimed.Owner != "bob" {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}
	if claimed.ClaimedAt == nil || claimed.LeaseExpiresAt == nil {
		t.Fatal("expected ClaimedAt and LeaseExpiresAt to be set")
	}

	initialLease := *claimed.LeaseExpiresAt
	time.Sleep(10 * time.Millisecond)
	heartbeated, err := s.Heartbeat("bob", task.ID, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeated.LeaseExpiresAt == nil || *heartbeated.LeaseExpiresAt <= initialLease {
		t.Fatalf("expected lease extension, got lease: %v, initial: %v", heartbeated.LeaseExpiresAt, initialLease)
	}

	result := map[string]any{"outcome": "ok"}
	completed, err := s.Complete("bob", task.ID, result)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "done" || completed.CompletedAt == nil {
		t.Fatalf("expected done status, got completed: %+v", completed)
	}
	if completed.Result.(map[string]any)["outcome"] != "ok" {
		t.Fatalf("expected result outcome=ok, got: %+v", completed.Result)
	}

	doneFiles, _ := listJSON(filepath.Join(s.RootPath(), "tasks", "done"))
	if len(doneFiles) != 1 {
		t.Fatalf("expected 1 done task file, got: %d", len(doneFiles))
	}
}

func TestTaskLeaseExpirySweep(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "test-task", nil)
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := s.Claim("bob", -time.Second, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("failed to claim task")
	}

	swept, err := s.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].ID != task.ID {
		t.Fatalf("expected to sweep task %s, got: %v", task.ID, swept)
	}

	sweptTask := swept[0]
	if sweptTask.Status != "open" || sweptTask.Owner != "" {
		t.Fatalf("expected swept task to be open with no owner, got: %+v", sweptTask)
	}
	if sweptTask.SweptFrom != "bob" || sweptTask.SweptAt == nil {
		t.Fatalf("expected SweptFrom='bob' and SweptAt set, got: %+v", sweptTask)
	}

	openFiles, _ := listJSON(filepath.Join(s.RootPath(), "tasks", "open"))
	if len(openFiles) != 1 {
		t.Fatalf("expected task to return to open, got: %d files", len(openFiles))
	}
}

func TestActorDoubleLock(t *testing.T) {
	s := New(t.TempDir())
	lock1, err := s.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	if lock1 == nil {
		t.Fatal("expected lock to be acquired")
	}

	lock2, err := s.LockActor("alice")
	if err == nil {
		_ = lock2.Close()
		t.Fatal("expected double lock for actor 'alice' to fail")
	}

	if err := lock1.Close(); err != nil {
		t.Fatal(err)
	}

	lock3, err := s.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	if lock3 == nil {
		t.Fatal("expected lock to succeed after first lock closed")
	}
	_ = lock3.Close()
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

func TestTaskAbandon(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "task", nil)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Claim("bob", time.Minute, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("expected task to be claimed")
	}

	abandoned, err := s.Abandon("bob", task.ID, "need help")
	if err != nil {
		t.Fatal(err)
	}
	if abandoned == nil || abandoned.Status != "open" || abandoned.Owner != "" {
		t.Fatalf("unexpected abandoned task: %+v", abandoned)
	}
	if abandoned.AbandonedReason != "need help" || abandoned.AbandonedBy != "bob" {
		t.Fatalf("unexpected abandon metadata: %+v", abandoned)
	}

	// Verify it can be claimed again
	reclaimed, err := s.Claim("carol", time.Minute, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed == nil || reclaimed.ID != task.ID {
		t.Fatalf("expected task to be reclaimed, got %v", reclaimed)
	}
}

func TestTaskClaimConcurrency(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "task", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Spin up multiple goroutines attempting to claim the same task concurrently
	const goroutines = 10
	errChan := make(chan error, goroutines)
	claimChan := make(chan *Task, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			actor := fmt.Sprintf("actor-%d", id)
			// Claim by ID to target the exact task
			claimed, err := s.Claim(actor, time.Minute, ClaimOptions{TaskID: task.ID})
			if err != nil {
				errChan <- err
				return
			}
			if claimed != nil {
				claimChan <- claimed
			} else {
				claimChan <- nil
			}
		}(i)
	}

	// Gather results
	claims := 0
	for i := 0; i < goroutines; i++ {
		select {
		case err := <-errChan:
			// Mismatch/already-claimed errors are expected for losers
			_ = err
		case claimed := <-claimChan:
			if claimed != nil {
				claims++
			}
		}
	}

	if claims != 1 {
		t.Fatalf("expected exactly 1 worker to claim the task, got %d", claims)
	}
}

func TestTaskSweepConcurrency(t *testing.T) {
	s := New(t.TempDir())
	task, err := s.Post("alice", "task", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Claim with negative TTL so it is immediately expirable
	claimed, err := s.Claim("bob", -time.Second, ClaimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("expected task to be claimed")
	}

	// Run concurrent Sweep and Heartbeat operations
	const iterations = 50
	for i := 0; i < iterations; i++ {
		// Reset task to claimed-expired state
		exp := unixFloat(time.Now().Add(-time.Second))
		claimed.Status = "claimed"
		claimed.Owner = "bob"
		claimed.LeaseExpiresAt = &exp
		claimed.ClaimedAt = &exp
		claimed.SweptAt = nil
		claimed.SweptFrom = ""

		path := filepath.Join(s.RootPath(), "tasks", "claimed", "bob", claimed.filename)
		// Clean folders
		_ = os.Remove(filepath.Join(s.RootPath(), "tasks", "open", claimed.filename))
		_ = os.Remove(filepath.Join(s.RootPath(), "tasks", "done", claimed.filename))
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		if err := writeJSON(path, claimed); err != nil {
			t.Fatal(err)
		}

		errChan := make(chan error, 2)
		go func() {
			_, err := s.Sweep()
			errChan <- err
		}()

		go func() {
			_, err := s.Heartbeat("bob", task.ID, time.Minute)
			errChan <- err
		}()

		// Wait for both goroutines
		for j := 0; j < 2; j++ {
			if err := <-errChan; err != nil {
				// We expect some operations to fail because of the race, e.g.
				// heartbeat might fail if swept, which returns "task lease lost".
				// This is correct! We just want to verify we don't crash, deadlock,
				// or end up with multiple files (duplicate task).
				_ = err
			}
		}

		// Verify that the file exists in exactly one place (either open or claimed)
		openExists := fileExists(filepath.Join(s.RootPath(), "tasks", "open", claimed.filename))
		claimedExists := fileExists(filepath.Join(s.RootPath(), "tasks", "claimed", "bob", claimed.filename))

		if openExists && claimedExists {
			t.Fatal("task exists concurrently in both open and claimed folders (race condition caused duplicate task file)")
		}
		if !openExists && !claimedExists {
			t.Fatal("task was lost during concurrent sweep and heartbeat")
		}
	}
}

func TestSweepClaimNoDuplicate(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := New(t.TempDir())
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		task, err := s.Post("alice", "task", nil)
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.Claim("bob", -time.Second, ClaimOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if c == nil {
			t.Fatal("expected task to be claimed")
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.Sweep()
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got, err := s.Claim("carol", time.Minute, ClaimOptions{})
				if err == nil && got != nil {
					return
				}
			}
		}()
		wg.Wait()

		// count files holding task.ID across tasks/open and tasks/claimed/*
		openCount := 0
		openFiles, _ := listJSON(filepath.Join(s.RootPath(), "tasks", "open"))
		for _, f := range openFiles {
			if strings.HasSuffix(f, "-"+task.ID+".json") {
				openCount++
			}
		}

		claimedCount := 0
		claimedRoot := filepath.Join(s.RootPath(), "tasks", "claimed")
		actors, _ := os.ReadDir(claimedRoot)
		for _, actorDir := range actors {
			if actorDir.IsDir() {
				cfiles, _ := listJSON(filepath.Join(claimedRoot, actorDir.Name()))
				for _, f := range cfiles {
					if strings.HasSuffix(f, "-"+task.ID+".json") {
						claimedCount++
					}
				}
			}
		}

		total := openCount + claimedCount
		if total > 1 {
			t.Fatalf("iteration %d: task duplicated in %d places (open: %d, claimed: %d)", i, total, openCount, claimedCount)
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestReapStaleTmpFilesMessageGuard(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}

	tmpDir := filepath.Join(s.RootPath(), "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Write a Message JSON to tmp/
	msgPath := filepath.Join(tmpDir, "msg-1.json.tmp-123456")
	msgJSON := `{"id":"msg-1","from":"alice","to":"bob","type":"ccrep:proposal","ts":1781109776.9}`
	if err := os.WriteFile(msgPath, []byte(msgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Write a valid claimed Task JSON to tmp/
	taskPath := filepath.Join(tmpDir, "task-1.json.tmp-789012")
	taskJSON := `{"id":"task-1","type":"wave-2","status":"claimed","created_at":1781107907.0}`
	if err := os.WriteFile(taskPath, []byte(taskJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Write a completed (done) Task JSON to tmp/
	donePath := filepath.Join(tmpDir, "task-done.json.tmp-345678")
	doneJSON := `{"id":"task-done","type":"wave-2","status":"done","result":{"outcome":"success"},"completed_at":1781107907.0}`
	if err := os.WriteFile(donePath, []byte(doneJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Backdate all three to be older than 5 minutes (e.g. 6 minutes ago)
	staleTime := time.Now().Add(-6 * time.Minute)
	if err := os.Chtimes(msgPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(taskPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(donePath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	// Run sweep which triggers reapStaleTmpFiles
	if _, err := s.Sweep(); err != nil {
		t.Fatal(err)
	}

	// Verify that the message tmp file was deleted and NOT recovered as a task
	if fileExists(msgPath) {
		t.Error("expected message tmp file to be deleted")
	}
	recoveredMsgPath := filepath.Join(s.RootPath(), "tasks", "open", "msg-1.json")
	if fileExists(recoveredMsgPath) {
		t.Error("expected message NOT to be recovered as a task in tasks/open/")
	}

	// Verify that the claimed task tmp file was recovered as a task in tasks/open/
	if fileExists(taskPath) {
		t.Error("expected task tmp file to be removed from tmp/")
	}
	recoveredTaskPath := filepath.Join(s.RootPath(), "tasks", "open", "task-1.json")
	if !fileExists(recoveredTaskPath) {
		t.Fatal("expected task to be recovered to tasks/open/")
	}

	// Verify the recovered task content
	recoveredTask, err := readTask(recoveredTaskPath)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredTask.Status != "open" {
		t.Errorf("expected recovered task status to be 'open', got %q", recoveredTask.Status)
	}
	if recoveredTask.ID != "task-1" {
		t.Errorf("expected recovered task ID to be 'task-1', got %q", recoveredTask.ID)
	}

	// Verify that the completed (done) task tmp file was recovered in tasks/done/
	if fileExists(donePath) {
		t.Error("expected done task tmp file to be removed from tmp/")
	}
	recoveredDonePath := filepath.Join(s.RootPath(), "tasks", "done", "task-done.json")
	if !fileExists(recoveredDonePath) {
		t.Fatal("expected done task to be recovered to tasks/done/")
	}
	recoveredOpenDonePath := filepath.Join(s.RootPath(), "tasks", "open", "task-done.json")
	if fileExists(recoveredOpenDonePath) {
		t.Error("expected done task NOT to be recovered to tasks/open/")
	}

	// Verify the recovered done task content and preserved result/completed_at
	recoveredDone, err := readTask(recoveredDonePath)
	if err != nil {
		t.Fatal(err)
	}
	if recoveredDone.Status != "done" {
		t.Errorf("expected recovered task status to be 'done', got %q", recoveredDone.Status)
	}
	if recoveredDone.ID != "task-done" {
		t.Errorf("expected recovered task ID to be 'task-done', got %q", recoveredDone.ID)
	}
	resObj, ok := recoveredDone.Result.(map[string]any)
	if !ok || resObj["outcome"] != "success" {
		t.Errorf("expected recovered task result to preserve outcome=success, got: %+v", recoveredDone.Result)
	}
	if recoveredDone.CompletedAt == nil || *recoveredDone.CompletedAt != 1781107907.0 {
		t.Errorf("expected recovered task CompletedAt to be preserved, got %v", recoveredDone.CompletedAt)
	}
}

