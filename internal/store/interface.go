package store

import (
	"context"
	"time"
)

type Store interface {
	RootPath() string
	Init() error
	EnsureActor(actor string) error
	IsActorLocked(actor string) bool
	LockActor(actor string) (*ActorLock, error)
	RollbackProcessing(actor string) error
	Send(from, to, typ string, payload map[string]any, inReplyTo string, expiresAt *float64) (Message, error)
	TryRecv(actor, matchType string) (*Message, error)
	Recv(ctx context.Context, actor, matchType string, timeout time.Duration) (*Message, error)
	Peek(actor, matchType string) (*Message, error)
	Ack(actor, msgID string, outcome any) (*Message, error)
	Seen(actor, msgID string) (bool, error)
	Inbox(actor string) (InboxSnapshot, error)
	Post(actor, typ string, payload map[string]any) (Task, error)
	Claim(actor string, leaseTTL time.Duration, opts ClaimOptions) (*Task, error)
	Complete(actor, taskID string, result any) (*Task, error)
	Heartbeat(actor, taskID string, leaseTTL time.Duration) (*Task, error)
	Abandon(actor, taskID, reason string) (*Task, error)
	Sweep() ([]Task, error)
	TaskCounts() (TaskCounts, error)

	// Session methods
	SessionNew(slug string, participants []string, creator string, decisionRule string, goals []string, guardrails []string) error
	SessionAppend(slug, actor, body string, handoff *SessionHandoff) (SessionEntry, error)
	SessionRead(slug, actor string, sinceTS float64, limit int) ([]SessionEntry, error)
	SessionList() ([]SessionMeta, error)
	SessionListAll() ([]SessionMeta, error)
	SessionRender(slug string) (string, error)
	SessionClose(slug, repoRoot string) error

	// Topic methods
	TopicPublish(topic, sender, body string) (TopicMessage, error)
	TopicRead(topic string, sinceID int64, limit int) ([]TopicMessage, error)
	TopicCursorUpdate(agent, topic string, lastReadID int64) error
	TopicCursorRead(agent, topic string) (int64, error)
}
