package ops

import (
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/memory"
)

func TestMergeForUpdatePreservesUnsetMetadata(t *testing.T) {
	existing := memory.Memory{
		Title:      "Discovery resolution",
		Status:     memory.StatusHistorical,
		Authors:    []string{"agy"},
		Created:    "2026-06-01",
		Scope:      memory.ScopeCrossFam,
		Type:       memory.TypeReference,
		Concepts:   []string{"discovery-resolution"},
		Supersedes: []string{"memory-old"},
		Body:       "old body",
	}
	// A bare `memory write --title X --body new` (no metadata flags changed).
	incoming := memory.Memory{
		Title:   "Discovery resolution",
		Status:  memory.StatusLive,
		Authors: []string{"claude"},
		Created: "2026-06-14",
		Scope:   memory.ScopeFam, // flag default, but not "changed"
		Body:    "new body",
	}
	changed := func(string) bool { return false }

	got := mergeForUpdate(incoming, existing, "claude", changed)

	// Preserved from existing:
	if got.Created != "2026-06-01" {
		t.Errorf("Created = %q, want preserved 2026-06-01", got.Created)
	}
	if got.Scope != memory.ScopeCrossFam {
		t.Errorf("Scope = %q, want preserved %q", got.Scope, memory.ScopeCrossFam)
	}
	if got.Type != memory.TypeReference {
		t.Errorf("Type = %q, want preserved %q", got.Type, memory.TypeReference)
	}
	if strings.Join(got.Concepts, ",") != "discovery-resolution" {
		t.Errorf("Concepts = %v, want preserved", got.Concepts)
	}
	if strings.Join(got.Supersedes, ",") != "memory-old" {
		t.Errorf("Supersedes = %v, want preserved", got.Supersedes)
	}
	// From the new write:
	if got.Body != "new body" {
		t.Errorf("Body = %q, want new body", got.Body)
	}
	if got.Status != memory.StatusLive {
		t.Errorf("Status = %q, want Live (write resurrects)", got.Status)
	}
	if got.Updated == "" {
		t.Error("Updated not stamped")
	}
	// Authors merged + sorted, deduped:
	if strings.Join(got.Authors, ",") != "agy,claude" {
		t.Errorf("Authors = %v, want [agy claude]", got.Authors)
	}
}

func TestMergeForUpdateHonorsExplicitOverride(t *testing.T) {
	existing := memory.Memory{
		Created:  "2026-06-01",
		Scope:    memory.ScopeCrossFam,
		Type:     memory.TypeReference,
		Concepts: []string{"old-concept"},
		Authors:  []string{"agy"},
	}
	incoming := memory.Memory{
		Scope:    memory.ScopeFam,
		Type:     memory.TypeProject,
		Concepts: []string{"new-concept"},
	}
	// Caller explicitly set scope/type/concepts (but not supersedes).
	changed := func(name string) bool {
		return name == "scope" || name == "type" || name == "concepts"
	}
	got := mergeForUpdate(incoming, existing, "claude", changed)

	if got.Scope != memory.ScopeFam {
		t.Errorf("Scope = %q, want overridden %q", got.Scope, memory.ScopeFam)
	}
	if got.Type != memory.TypeProject {
		t.Errorf("Type = %q, want overridden %q", got.Type, memory.TypeProject)
	}
	if strings.Join(got.Concepts, ",") != "new-concept" {
		t.Errorf("Concepts = %v, want overridden", got.Concepts)
	}
	// supersedes not changed -> inherits existing (nil here)
	if len(got.Supersedes) != 0 {
		t.Errorf("Supersedes = %v, want empty (inherited)", got.Supersedes)
	}
}
