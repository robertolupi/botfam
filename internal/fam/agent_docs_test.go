package fam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderAgentDocsIncludesSkillRoster(t *testing.T) {
	repoRoot := t.TempDir()
	writeTestSkill(t, repoRoot, "zeta", "zeta-skill", "Handle zeta work.")
	writeTestSkill(t, repoRoot, "alpha", "alpha-skill", "Handle alpha work.")

	got, err := RenderAgentDocs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)

	if !strings.Contains(text, "Generated from `skills/*/SKILL.md`.") {
		t.Fatalf("rendered docs missing generated roster marker:\n%s", text)
	}
	alpha := strings.Index(text, "- `alpha-skill`: Handle alpha work.")
	zeta := strings.Index(text, "- `zeta-skill`: Handle zeta work.")
	if alpha < 0 || zeta < 0 {
		t.Fatalf("rendered docs missing expected skills:\n%s", text)
	}
	if alpha > zeta {
		t.Fatalf("skills not sorted by name:\n%s", text)
	}
}

func TestGenerateAndCheckAgentDocs(t *testing.T) {
	repoRoot := t.TempDir()
	writeTestSkill(t, repoRoot, "retro", "botfam-session-retrospective", "Write session retrospectives.")

	if err := GenerateAgentDocs(repoRoot); err != nil {
		t.Fatal(err)
	}
	stale, err := CheckAgentDocs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected generated docs to be fresh, got stale files: %v", stale)
	}

	if err := os.WriteFile(filepath.Join(repoRoot, "CLAUDE.md"), []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stale, err = CheckAgentDocs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "CLAUDE.md" {
		t.Fatalf("expected only CLAUDE.md to be stale, got %v", stale)
	}
}

func TestRenderAgentDocsRejectsMalformedSkill(t *testing.T) {
	repoRoot := t.TempDir()
	dir := filepath.Join(repoRoot, "skills", "bad")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: bad\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := RenderAgentDocs(repoRoot)
	if err == nil {
		t.Fatal("expected malformed skill to fail")
	}
	if !strings.Contains(err.Error(), "frontmatter missing description") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func writeTestSkill(t *testing.T, repoRoot, dir, name, desc string) {
	t.Helper()
	skillDir := filepath.Join(repoRoot, "skills", dir)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
