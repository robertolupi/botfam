package observe_test

import (
	"context"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/eventdelivery/observe"
)

// TestPollHeadSHADetectsForcePush proves the force-push gap mitigation: the
// first head SHA is seeded (no work), an unchanged SHA is a no-op, and a changed
// SHA dispatches a rebuild.
func TestPollHeadSHADetectsForcePush(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	q := newFakeDetail()
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(time.Now()))

	// First observation of the head SHA: seeded, not dispatched.
	emitted, err := tr.PollHeadSHA(ctx, db, "run-1", 476, "aaaa1111", 1)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("first head SHA should be seeded, not dispatched")
	}

	// Same SHA again: no-op.
	emitted, err = tr.PollHeadSHA(ctx, db, "run-1", 476, "aaaa1111", 1)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("unchanged head SHA should not dispatch")
	}

	// Changed SHA (force-push): dispatched.
	emitted, err = tr.PollHeadSHA(ctx, db, "run-1", 476, "bbbb2222", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("changed head SHA should dispatch a rebuild")
	}

	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items WHERE kind = ?`, observe.WorkRebuildPR); got != 1 {
		t.Fatalf("rebuild work items = %d, want 1", got)
	}
}

// TestPollCommitStatusSeedsThenEmits proves CI status polling seeds the first
// observed state and dispatches a check only when the (sha,state) changes.
func TestPollCommitStatusSeedsThenEmits(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	q := newFakeDetail()
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(time.Now()))

	q.status["headsha"] = "pending"
	emitted, err := tr.PollCommitStatus(ctx, db, "run-1", 476, "headsha", 1)
	if err != nil {
		t.Fatal(err)
	}
	if emitted {
		t.Fatal("first CI status should be seeded, not dispatched")
	}

	q.status["headsha"] = "failure"
	emitted, err = tr.PollCommitStatus(ctx, db, "run-1", 476, "headsha", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !emitted {
		t.Fatal("changed CI status should dispatch a check")
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items WHERE kind = ?`, observe.WorkCheckFailedRun); got != 1 {
		t.Fatalf("check work items = %d, want 1", got)
	}
}
