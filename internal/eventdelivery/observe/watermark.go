package observe

import "time"

// SessionWatermark is the live-push baseline for one supervisor session.
//
// It is deliberately ephemeral and session-scoped: it lives only in memory for
// the lifetime of the run and is never written to the forge or to a shared
// spool. The watermark begins at session/sprint start, and only events that
// occur strictly after that point are treated as live wakes.
//
// The watermark gates the *live push* stream only. Standing work (assigned /
// review-requested / authored-open) is recovered by the worklist queries and is
// never filtered by the watermark, and the pre-session unread bootstrap is
// imported regardless of the watermark. This is what makes the cursor able to
// only ever over-wake, never under-wake: anything the watermark might drop is
// independently recoverable by query.
type SessionWatermark struct {
	start time.Time
}

// NewSessionWatermark returns a watermark anchored at the session/sprint start.
// Callers pass time.Now() at boot; the watermark is not persisted across runs.
func NewSessionWatermark(start time.Time) SessionWatermark {
	return SessionWatermark{start: start.UTC()}
}

// Start is the session/sprint start instant the watermark is anchored at.
func (w SessionWatermark) Start() time.Time { return w.start }

// IsLiveWake reports whether an event occurring at t should be delivered as a
// live wake — i.e. it happened strictly after the session start. Events at or
// before the start are not live wakes; standing work covering them is recovered
// by the worklist query instead.
func (w SessionWatermark) IsLiveWake(t time.Time) bool {
	return t.After(w.start)
}
