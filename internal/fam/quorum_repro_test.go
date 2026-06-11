package fam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupMockRegistry creates a temporary fam.toml and returns the directory path.
func setupMockRegistry(t *testing.T, roster []string) string {
	dir := t.TempDir()
	var quotedRoster []string
	for _, r := range roster {
		quotedRoster = append(quotedRoster, fmt.Sprintf("%q", r))
	}
	tomlContent := fmt.Sprintf(`name = "mockfam"
created_at = "2026-06-09T19:20:27Z"
root_set = ["1b83d566729c24261e3ade7155c40fc5b37dd2cd"]
roster = [%s]
repo_paths = ["/Users/rlupi/src/botfam"]
object_stores = ["/Users/rlupi/src/botfam/.git/objects"]
`, strings.Join(quotedRoster, ", "))

	err := os.WriteFile(filepath.Join(dir, "fam.toml"), []byte(tomlContent), 0644)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Reproduction for review finding B2 (claude, 2026-06-11): TallyProposal
// ignores the proposal's quorum= field. With quorum=all and three known
// reviewers, a single approval must NOT yield APPROVED.
func TestTallyHonorsQuorumAll(t *testing.T) {
	mockRoot := setupMockRegistry(t, []string{"codex", "claude", "agy"})
	t.Setenv("COLLAB_ROOT", mockRoot)

	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")

	lines := []string{
		// codex is present (JOIN, no QUIT) but never votes: quorum=all unmet.
		`{"timestamp":"2026-06-11T07:59:00Z","sender":"codex","type":"JOIN","target":"#ccrep"}`,
		`{"timestamp":"2026-06-11T08:00:00Z","sender":"claude","type":"PRIVMSG","target":"#ccrep","body":"!propose id=p-q sha=abc1234 quorum=all deadline=2030-06-11T23:00:00+02:00 summary=test"}`,
		`{"timestamp":"2026-06-11T08:01:00Z","sender":"agy","type":"PRIVMSG","target":"#ccrep","body":"!vote id=p-q sha=abc1234 verdict=approve"}`,
	}
	if err := os.WriteFile(history, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	summary, err := TallyProposal(history, "p-q")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, "status: APPROVED") {
		t.Fatalf("quorum=all with 1 of 2 independent reviewers voting must not be APPROVED; got: %s", summary)
	}
}

func TestTallyQuorumAllWithPart(t *testing.T) {
	mockRoot := setupMockRegistry(t, []string{"codex", "claude", "agy"})
	t.Setenv("COLLAB_ROOT", mockRoot)

	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")

	lines := []string{
		`{"timestamp":"2026-06-11T07:59:00Z","sender":"codex","type":"JOIN","target":"#ccrep"}`,
		`{"timestamp":"2026-06-11T08:00:00Z","sender":"claude","type":"PRIVMSG","target":"#ccrep","body":"!propose id=p-q sha=abc1234 quorum=all deadline=2030-06-11T23:00:00+02:00 summary=test"}`,
		`{"timestamp":"2026-06-11T08:01:00Z","sender":"agy","type":"PRIVMSG","target":"#ccrep","body":"!vote id=p-q sha=abc1234 verdict=approve"}`,
		// codex parts #ccrep, so they are no longer present.
		`{"timestamp":"2026-06-11T08:02:00Z","sender":"codex","type":"PART","target":"#ccrep"}`,
	}
	if err := os.WriteFile(history, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	summary, err := TallyProposal(history, "p-q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") {
		t.Fatalf("quorum=all should be APPROVED since all present independent members approved; got: %s", summary)
	}
}

func TestTallyQuorumAllWithNickChange(t *testing.T) {
	mockRoot := setupMockRegistry(t, []string{"codex-new", "claude", "agy"})
	t.Setenv("COLLAB_ROOT", mockRoot)

	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")

	lines := []string{
		`{"timestamp":"2026-06-11T07:59:00Z","sender":"codex","type":"JOIN","target":"#ccrep"}`,
		`{"timestamp":"2026-06-11T08:00:00Z","sender":"claude","type":"PRIVMSG","target":"#ccrep","body":"!propose id=p-q sha=abc1234 quorum=all deadline=2030-06-11T23:00:00+02:00 summary=test"}`,
		`{"timestamp":"2026-06-11T08:01:00Z","sender":"agy","type":"PRIVMSG","target":"#ccrep","body":"!vote id=p-q sha=abc1234 verdict=approve"}`,
		// codex changes nick to codex-new
		`{"timestamp":"2026-06-11T08:02:00Z","sender":"codex","type":"NICK","target":"codex-new"}`,
	}
	if err := os.WriteFile(history, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// First verify it's not approved yet (codex-new is present but hasn't voted)
	summary, err := TallyProposal(history, "p-q")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(summary, "status: APPROVED") {
		t.Fatalf("quorum=all should NOT be APPROVED before codex-new votes; got: %s", summary)
	}

	// Now codex-new votes approve
	appendLine := `{"timestamp":"2026-06-11T08:03:00Z","sender":"codex-new","type":"PRIVMSG","target":"#ccrep","body":"!vote id=p-q sha=abc1234 verdict=approve"}`
	f, err := os.OpenFile(history, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte(appendLine + "\n"))
	f.Close()

	summary, err = TallyProposal(history, "p-q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") {
		t.Fatalf("quorum=all should be APPROVED after codex-new votes; got: %s", summary)
	}
}

func TestTallyDeadlineExpiration(t *testing.T) {
	mockRoot := setupMockRegistry(t, []string{"codex", "claude", "agy"})
	t.Setenv("COLLAB_ROOT", mockRoot)

	dir := t.TempDir()
	history := filepath.Join(dir, "history.jsonl")

	lines := []string{
		`{"timestamp":"2026-06-11T07:59:00Z","sender":"codex","type":"JOIN","target":"#ccrep"}`,
		// Proposal with past deadline
		`{"timestamp":"2026-06-11T08:00:00Z","sender":"claude","type":"PRIVMSG","target":"#ccrep","body":"!propose id=p-q sha=abc1234 quorum=all deadline=2020-06-11T12:00:00Z summary=test"}`,
		`{"timestamp":"2026-06-11T08:01:00Z","sender":"agy","type":"PRIVMSG","target":"#ccrep","body":"!vote id=p-q sha=abc1234 verdict=approve"}`,
	}
	if err := os.WriteFile(history, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	summary, err := TallyProposal(history, "p-q")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: EXPIRED") {
		t.Fatalf("proposal with past deadline should be EXPIRED; got: %s", summary)
	}
}
