package store

type SessionMeta struct {
	Slug         string   `json:"slug"`
	Participants []string `json:"participants"`
	CreatedBy    string   `json:"created_by"`
	CreatedAt    float64  `json:"created_at"`
	DecisionRule string   `json:"decision_rule,omitempty"`
	Goals        []string `json:"goals,omitempty"`
	Guardrails   []string `json:"guardrails,omitempty"`
	Archived     bool     `json:"archived,omitempty"`
}

type SessionHandoff struct {
	Task        string `json:"task"`
	Context     string `json:"context"`
	Deliverable string `json:"deliverable"`
}

type SessionEntry struct {
	ID      string          `json:"id"`
	Actor   string          `json:"actor"`
	TS      float64         `json:"ts"`
	Body    string          `json:"body"`
	Handoff *SessionHandoff `json:"handoff,omitempty"`
}
