package observe_test

import (
	"context"
	"testing"

	"github.com/robertolupi/botfam/internal/eventdelivery/observe"
)

func TestCloseMergeGate(t *testing.T) {
	ctx := context.Background()
	q := newFakeDetail()
	q.blockers[500] = []observe.Blocker{
		{Number: 1, State: "open"},   // open + out-of-scope → holds
		{Number: 2, State: "closed"}, // resolved → ignored
		{Number: 3, State: "open"},   // open but in-scope → allowed
	}

	blocked, offending, err := observe.CloseMergeGate(ctx, q, 500, map[int64]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("expected close/merge to be blocked by open out-of-scope dependency #1")
	}
	if len(offending) != 1 || offending[0].Number != 1 {
		t.Fatalf("offending = %+v, want only #1", offending)
	}
}

func TestCloseMergeGateAllowedWhenDepsResolvedOrInScope(t *testing.T) {
	ctx := context.Background()
	q := newFakeDetail()

	// No dependencies at all.
	if blocked, _, err := observe.CloseMergeGate(ctx, q, 600, nil); err != nil || blocked {
		t.Fatalf("no deps: blocked=%v err=%v, want allowed", blocked, err)
	}

	// All blockers closed, or open-but-in-scope.
	q.blockers[601] = []observe.Blocker{
		{Number: 10, State: "closed"},
		{Number: 11, State: "open"},
	}
	if blocked, _, err := observe.CloseMergeGate(ctx, q, 601, map[int64]bool{11: true}); err != nil || blocked {
		t.Fatalf("resolved/in-scope: blocked=%v err=%v, want allowed", blocked, err)
	}
}
