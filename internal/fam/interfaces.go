package fam

import "context"

// Plane represents one of the four blackboards in the botfam plane ontology.
type Plane string

const (
	PlaneForge Plane = "forge" // Control Plane (Gitea API, mailboxes)
	PlaneRepo  Plane = "repo"  // Data Plane (Git files, branches)
	PlaneIRC   Plane = "irc"   // Ephemeral Side Channel (Real-time wakes/presence)
	PlaneWiki  Plane = "wiki"  // Advisory Side Channel (Rationale/Design wiki pages)
)

// Blackboard abstracts reading and writing facts across different planes.
type Blackboard interface {
	ReadFact(ctx context.Context, plane Plane, key string) (string, error)
	WriteFact(ctx context.Context, plane Plane, key string, val string) error
}

// ClaimManager abstracts atomic task assignments and claims on the Control Plane (Forge).
type ClaimManager interface {
	ClaimTask(ctx context.Context, taskID string, agentID string) error
	ReleaseTask(ctx context.Context, taskID string, agentID string) error
	GetActiveClaims(ctx context.Context, agentID string) ([]string, error)
}

// MergeStatus represents the resolution state of a merge queue request.
type MergeStatus string

const (
	MergeStatusPending  MergeStatus = "pending"
	MergeStatusMerged   MergeStatus = "merged"
	MergeStatusConflict MergeStatus = "conflict"
	MergeStatusFailed   MergeStatus = "failed"
)

// MergeQueue serializes merges to the repository tip, protecting the data plane from concurrent rebase churn.
type MergeQueue interface {
	EnqueueMerge(ctx context.Context, prID string, baseSHA string, headSHA string) (string, error)
	GetMergeStatus(ctx context.Context, prID string) (MergeStatus, error)
}

// HandoverManager handles warm checkpoints for Let-It-Crash agent recycling, preventing re-paying the onboarding tax.
type HandoverManager interface {
	SaveCheckpoint(ctx context.Context, taskID string, state []byte) error
	LoadCheckpoint(ctx context.Context, taskID string) ([]byte, error)
}
