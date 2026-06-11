package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

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

// SessionNew initializes a new session directory and writes meta.json.
func (s *MaildirStore) SessionNew(slug string, participants []string, creator string, decisionRule string, goals []string, guardrails []string) error {
	if err := ValidateName("session slug", slug); err != nil {
		return err
	}
	for _, p := range participants {
		if err := ValidateName("participant", p); err != nil {
			return err
		}
	}
	dir := filepath.Join(s.Root, "sessions", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	metaPath := filepath.Join(dir, "meta.json")
	if _, err := os.Stat(metaPath); err == nil {
		return fmt.Errorf("session %q already exists", slug)
	}
	meta := SessionMeta{
		Slug:         slug,
		Participants: participants,
		CreatedBy:    creator,
		CreatedAt:    unixFloat(time.Now().UTC()),
		DecisionRule: decisionRule,
		Goals:        goals,
		Guardrails:   guardrails,
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(metaPath, b, 0o644)
}

// SessionAppend appends an entry to session.jsonl inside an exclusive lock.
func (s *MaildirStore) SessionAppend(slug, actor, body string, handoff *SessionHandoff) (SessionEntry, error) {
	if err := ValidateName("session slug", slug); err != nil {
		return SessionEntry{}, err
	}
	if err := ValidateName("actor", actor); err != nil {
		return SessionEntry{}, err
	}
	if !s.IsActorLocked(actor) {
		return SessionEntry{}, fmt.Errorf("actor %q is not locked by this process", actor)
	}
	if handoff != nil {
		if strings.TrimSpace(handoff.Task) == "" {
			return SessionEntry{}, errors.New("invalid handoff: task cannot be empty or whitespace only")
		}
		if strings.TrimSpace(handoff.Context) == "" {
			return SessionEntry{}, errors.New("invalid handoff: context cannot be empty or whitespace only")
		}
		if strings.TrimSpace(handoff.Deliverable) == "" {
			return SessionEntry{}, errors.New("invalid handoff: deliverable cannot be empty or whitespace only")
		}
	}

	dir := filepath.Join(s.Root, "sessions", slug)
	metaPath := filepath.Join(dir, "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		return SessionEntry{}, fmt.Errorf("session %q does not exist", slug)
	}

	entry := SessionEntry{
		ID:      id(),
		Actor:   actor,
		TS:      unixFloat(time.Now().UTC()),
		Body:    body,
		Handoff: handoff,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return SessionEntry{}, err
	}
	line = append(line, '\n')

	jsonlPath := filepath.Join(dir, "session.jsonl")
	f, err := os.OpenFile(jsonlPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return SessionEntry{}, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return SessionEntry{}, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	if _, err := f.Write(line); err != nil {
		return SessionEntry{}, err
	}
	if err := f.Sync(); err != nil {
		return SessionEntry{}, err
	}

	return entry, nil
}

// SessionRead reads entries from session.jsonl with an optional actor and timestamp filter.
func (s *MaildirStore) SessionRead(slug, actor string, sinceTS float64, limit int) ([]SessionEntry, error) {
	if err := ValidateName("session slug", slug); err != nil {
		return nil, err
	}
	jsonlPath := filepath.Join(s.Root, "sessions", slug, "session.jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			metaPath := filepath.Join(s.Root, "sessions", slug, "meta.json")
			if _, statErr := os.Stat(metaPath); statErr == nil {
				return nil, nil
			}
			return nil, fmt.Errorf("session %q does not exist", slug)
		}
		return nil, err
	}
	defer f.Close()

	var entries []SessionEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal([]byte(text), &entry); err != nil {
			// Skip torn lines or invalid records
			continue
		}
		if actor != "" && entry.Actor != actor {
			continue
		}
		if sinceTS > 0 && entry.TS <= sinceTS {
			continue
		}
		entries = append(entries, entry)
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	return entries, nil
}

// SessionList returns metadata for all active (non-archived) sessions.
func (s *MaildirStore) SessionList() ([]SessionMeta, error) {
	dir := filepath.Join(s.Root, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var active []SessionMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		if _, err := os.Stat(filepath.Join(dir, slug, "ARCHIVED")); err == nil {
			continue
		}

		metaPath := filepath.Join(dir, slug, "meta.json")
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(b, &meta); err == nil {
			active = append(active, meta)
		}
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].CreatedAt > active[j].CreatedAt
	})

	return active, nil
}

// SessionListAll returns metadata for all sessions (both active and archived).
func (s *MaildirStore) SessionListAll() ([]SessionMeta, error) {
	dir := filepath.Join(s.Root, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var all []SessionMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		metaPath := filepath.Join(dir, slug, "meta.json")
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(b, &meta); err == nil {
			if _, err := os.Stat(filepath.Join(dir, slug, "ARCHIVED")); err == nil {
				meta.Archived = true
			}
			all = append(all, meta)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})

	return all, nil
}


// SessionRender projects a session.jsonl file into Markdown format.
func (s *MaildirStore) SessionRender(slug string) (string, error) {
	entries, err := s.SessionRead(slug, "", 0, 0)
	if err != nil {
		return "", err
	}

	metaPath := filepath.Join(s.Root, "sessions", slug, "meta.json")
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return "", err
	}
	var meta SessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString("<!-- RENDERED by botfam session render — DO NOT EDIT (append via session_append) -->\n\n")
	out.WriteString(fmt.Sprintf("# Session: %s\n\n", slug))
	out.WriteString("## Participants\n\n")
	for _, p := range meta.Participants {
		out.WriteString(fmt.Sprintf("- %s\n", p))
	}
	out.WriteString("\n---\n")

	for _, entry := range entries {
		out.WriteString("\n")
		t := time.Unix(0, int64(entry.TS*1e9)).UTC()
		out.WriteString(fmt.Sprintf("## [%s, %s]\n", entry.Actor, t.Format(time.RFC3339)))
		out.WriteString(strings.TrimSpace(entry.Body))
		out.WriteString("\n")

		if entry.Handoff != nil {
			out.WriteString("\n**→ Handoff:**\n")
			out.WriteString(fmt.Sprintf("**Task:** %s\n", entry.Handoff.Task))
			out.WriteString(fmt.Sprintf("**Context:** %s\n", entry.Handoff.Context))
			out.WriteString(fmt.Sprintf("**Deliverable:** %s\n", entry.Handoff.Deliverable))
		}
	}

	return out.String(), nil
}

// SessionClose renders a session and writes it into the specified repo worktree.
func (s *MaildirStore) SessionClose(slug, repoRoot string) error {
	rendered, err := s.SessionRender(slug)
	if err != nil {
		return err
	}

	destDir := filepath.Join(repoRoot, "doc", "collab", "sessions", slug)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	destFile := filepath.Join(destDir, "session.md")
	return os.WriteFile(destFile, []byte(rendered), 0o644)
}
