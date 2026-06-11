package store

import "time"

type Message struct {
	ID        string         `json:"id"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	TS        float64        `json:"ts"`
	InReplyTo string         `json:"in_reply_to,omitempty"`
	ExpiresAt *float64       `json:"expires_at,omitempty"`
	Outcome   any            `json:"outcome,omitempty"`

	filename string
}

type Task struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	Payload         map[string]any `json:"payload"`
	Status          string         `json:"status"`
	Owner           string         `json:"owner,omitempty"`
	CreatedAt       float64        `json:"created_at"`
	ClaimedAt       *float64       `json:"claimed_at,omitempty"`
	LeaseExpiresAt  *float64       `json:"lease_expires_at,omitempty"`
	Result          any            `json:"result,omitempty"`
	CompletedAt     *float64       `json:"completed_at,omitempty"`
	AbandonedAt     *float64       `json:"abandoned_at,omitempty"`
	AbandonedBy     string         `json:"abandoned_by,omitempty"`
	AbandonedReason string         `json:"abandoned_reason,omitempty"`
	SweptAt         *float64       `json:"swept_at,omitempty"`
	SweptFrom       string         `json:"swept_from,omitempty"`

	filename string
}

type InboxSnapshot struct {
	Actor      string     `json:"actor"`
	New        []string   `json:"new"`
	Processing []string   `json:"processing"`
	Cur        []string   `json:"cur"`
	Tasks      TaskCounts `json:"tasks"`
}

type TaskCounts struct {
	Open    int            `json:"open"`
	Claimed map[string]int `json:"claimed"`
	Done    int            `json:"done"`
}

type ClaimOptions struct {
	TaskID         string
	Type           string
	SuggestedOwner string
}

type TopicMessage struct {
	ID    int64   `json:"id"`
	Topic string  `json:"topic"`
	From  string  `json:"from"`
	TS    float64 `json:"ts"`
	Body  string  `json:"body"`
}

func unixFloat(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}
