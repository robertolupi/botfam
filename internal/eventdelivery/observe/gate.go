package observe

import "context"

// CloseMergeGate reports whether a close/merge mutation on the given in-scope
// artifact must be held. It is blocked when a dependency (blocker) is both
// unresolved (open) and out-of-scope — i.e. the blocker is not in the current
// scope generation, so it is flagged needs_refresh rather than known-done.
//
// inScope is the set of artifact numbers in the current scope generation. An
// empty/nil set means nothing is in scope, so any open blocker holds the action.
// Closed blockers never hold, and open blockers that are themselves in scope are
// allowed (they will be handled within the same scope).
//
// This is the supervisor-internal gate the action path consults before executing
// a close or merge through the outbox; it does not itself perform any mutation.
func CloseMergeGate(ctx context.Context, q DetailQuerier, number int64, inScope map[int64]bool) (blocked bool, offending []Blocker, err error) {
	blockers, err := q.ListBlockers(ctx, number)
	if err != nil {
		return false, nil, err
	}
	for _, b := range blockers {
		if b.Open() && !inScope[b.Number] {
			offending = append(offending, b)
		}
	}
	return len(offending) > 0, offending, nil
}
