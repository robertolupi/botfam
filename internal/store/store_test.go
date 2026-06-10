package store

import (
	"path/filepath"
	"reflect"
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
	newFiles, err := listJSON(filepath.Join(s.Root, "bob", "new"))
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
	procFiles, err := listJSON(filepath.Join(s.Root, "bob", "processing"))
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
	curFiles, err := listJSON(filepath.Join(s.Root, "bob", "cur"))
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

	procFiles, _ := listJSON(filepath.Join(s.Root, "bob", "processing"))
	if len(procFiles) != 1 {
		t.Fatalf("expected message to be in processing, got %d files", len(procFiles))
	}

	if err := s.RollbackProcessing("bob"); err != nil {
		t.Fatal(err)
	}

	procFiles, _ = listJSON(filepath.Join(s.Root, "bob", "processing"))
	if len(procFiles) != 0 {
		t.Fatalf("expected processing to be empty, got %d files", len(procFiles))
	}
	newFiles, _ := listJSON(filepath.Join(s.Root, "bob", "new"))
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

	expiredFiles, err := listJSON(filepath.Join(s.Root, "bob", "expired"))
	if err != nil {
		t.Fatal(err)
	}
	if len(expiredFiles) != 1 {
		t.Fatalf("expected 1 file in expired, got: %d", len(expiredFiles))
	}

	newFiles, _ := listJSON(filepath.Join(s.Root, "bob", "new"))
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

	openFiles, _ := listJSON(filepath.Join(s.Root, "tasks", "open"))
	if len(openFiles) != 1 {
		t.Fatalf("expected 1 open task file, got: %d", len(openFiles))
	}

	claimed, err := s.Claim("bob", time.Minute)
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

	doneFiles, _ := listJSON(filepath.Join(s.Root, "tasks", "done"))
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

	claimed, err := s.Claim("bob", -time.Second)
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

	openFiles, _ := listJSON(filepath.Join(s.Root, "tasks", "open"))
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

func TestClaimByIDContract(t *testing.T) {
	s := New(t.TempDir())
	
	stVal := reflect.ValueOf(s)
	claimMethod := stVal.MethodByName("Claim")
	
	var claimByIDFunc func(actor, taskID string, leaseTTL time.Duration) (*Task, error)
	
	if claimMethod.IsValid() {
		mType := claimMethod.Type()
		if mType.NumIn() == 3 && mType.In(2).Kind() == reflect.String {
			claimByIDFunc = func(actor, taskID string, leaseTTL time.Duration) (*Task, error) {
				res := claimMethod.Call([]reflect.Value{
					reflect.ValueOf(actor),
					reflect.ValueOf(leaseTTL),
					reflect.ValueOf(taskID),
				})
				if !res[1].IsNil() {
					return nil, res[1].Interface().(error)
				}
				if res[0].IsNil() {
					return nil, nil
				}
				return res[0].Interface().(*Task), nil
			}
		}
	}
	
	claimByIDMethod := stVal.MethodByName("ClaimByID")
	if claimByIDMethod.IsValid() {
		mType := claimByIDMethod.Type()
		if mType.NumIn() == 3 {
			claimByIDFunc = func(actor, taskID string, leaseTTL time.Duration) (*Task, error) {
				var args []reflect.Value
				if mType.In(1).Kind() == reflect.String {
					args = []reflect.Value{
						reflect.ValueOf(actor),
						reflect.ValueOf(taskID),
						reflect.ValueOf(leaseTTL),
					}
				} else {
					args = []reflect.Value{
						reflect.ValueOf(actor),
						reflect.ValueOf(leaseTTL),
						reflect.ValueOf(taskID),
					}
				}
				res := claimByIDMethod.Call(args)
				if !res[1].IsNil() {
					return nil, res[1].Interface().(error)
				}
				if res[0].IsNil() {
					return nil, nil
				}
				return res[0].Interface().(*Task), nil
			}
		}
	}
	
	if claimByIDFunc == nil {
		t.Skip("claim-by-id feature from W1-A is not detected in store signature yet")
	}
	
	_, err := s.Post("alice", "task", map[string]any{"name": "A"})
	if err != nil {
		t.Fatal(err)
	}
	taskB, err := s.Post("alice", "task", map[string]any{"name": "B"})
	if err != nil {
		t.Fatal(err)
	}
	
	claimed, err := claimByIDFunc("bob", taskB.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != taskB.ID {
		t.Fatalf("expected to claim task B (%s), got %v", taskB.ID, claimed)
	}
	
	counts, err := s.TaskCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts.Open != 1 {
		t.Fatalf("expected 1 open task (A), got %d", counts.Open)
	}
}
