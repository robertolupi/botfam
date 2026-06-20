// Package observe implements the supervisor-internal observation layer of
// EventDelivery v2: the standing worklist query generators, the pre-session
// unread notification bootstrap, and the session-scoped watermark lifecycle.
//
// It produces RawObservation candidates from the forge and ingests them into the
// session store's raw_observations table. It is supervisor-internal: nothing
// here is agent-facing. Translation of raw observations into event-level work
// items (the thread→event diff) is a separate concern recorded against the same
// store.
//
// See the wiki page EventDeliveryV2NotificationService for the design.
package observe

import (
	"strconv"
	"strings"
)

// Source is the observation origin. Only the forge exists today.
const Source = "forge"

// Artifact kinds.
const (
	KindIssue = "issue"
	KindPull  = "pull"
)

// Event classes, mirroring the raw_observations.event_class CHECK constraint.
// stable-id events carry an immutable forge identifier; synthetic-id events are
// advisory and deduped conservatively; query-only events have no notification
// thread and are recovered purely by polling a standing condition.
const (
	ClassStableID    = "stable-id"
	ClassSyntheticID = "synthetic-id"
	ClassQueryOnly   = "query-only"
)

// Event kinds emitted by this layer.
const (
	// EventAssignedOpen is the standing "this open artifact is assigned to me"
	// condition. Its key is constant per artifact so re-polling an unchanged
	// artifact does not mint a new observation.
	EventAssignedOpen = "assigned_open"
	// EventReviewRequested is the standing "I am a requested reviewer" condition.
	EventReviewRequested = "review_requested"
	// EventPush records an authored PR's head SHA; a changed SHA is a new event.
	EventPush = "push"
	// EventNotification is an imported unread notification thread (the unread
	// bootstrap backstop), keyed on the thread's updated_at.
	EventNotification = "notification"
)

// Observation is one raw observation candidate. Its fields map onto the
// raw_observations table columns and the RawObservation proto.
type Observation struct {
	Source               string
	Repo                 string // "owner/repo"
	ArtifactKind         string // KindIssue | KindPull
	ArtifactNumber       int64
	NotificationThreadID string // empty unless derived from a notification thread
	EventKind            string
	EventKey             string
	EventClass           string
	SourceQuery          string
	PayloadJSON          string
}

// EventID is the deterministic identity that survives inbox rebuilds:
//
//	<source>:<repo>:<artifact-kind>:<number>:<event-kind>:<event-key>
//
// It is used as the raw_observations primary key. Re-polling an unchanged
// artifact yields the same EventID (no new id), and the store's UNIQUE
// constraint on the component columns provides the same guarantee.
func (o Observation) EventID() string {
	return strings.Join([]string{
		o.Source,
		o.Repo,
		o.ArtifactKind,
		strconv.FormatInt(o.ArtifactNumber, 10),
		o.EventKind,
		o.EventKey,
	}, ":")
}
