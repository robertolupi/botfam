package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
)

func TestFamChannels(t *testing.T) {
	cases := []struct {
		name      string
		reg       Registry
		wantMain  string
		wantCcrep string
	}{
		{
			name:      "no registry falls back to legacy literals",
			reg:       Registry{},
			wantMain:  "#botfam",
			wantCcrep: "#ccrep",
		},
		{
			name:      "defaults derive from fam name",
			reg:       Registry{Name: "deep-cuts"},
			wantMain:  "#deep-cuts",
			wantCcrep: "#deep-cuts-ccrep",
		},
		{
			name:      "explicit channels win over derivation",
			reg:       Registry{Name: "botfam", Channels: []string{"#botfam", "#ccrep"}},
			wantMain:  "#botfam",
			wantCcrep: "#ccrep",
		},
		{
			name:      "single explicit channel derives ccrep from name",
			reg:       Registry{Name: "deep-cuts", Channels: []string{"#dc-main"}},
			wantMain:  "#dc-main",
			wantCcrep: "#deep-cuts-ccrep",
		},
		{
			name:      "named fam without channels derives both",
			reg:       Registry{Name: "botfam"},
			wantMain:  "#botfam",
			wantCcrep: "#botfam-ccrep",
		},
		{
			name:      "explicit slug wins over name for derivation",
			reg:       Registry{Name: "deep-cuts", Slug: "dc"},
			wantMain:  "#dc",
			wantCcrep: "#dc-ccrep",
		},
		{
			name:      "explicit channels win over slug derivation",
			reg:       Registry{Name: "deep-cuts", Slug: "dc", Channels: []string{"#deep-cuts", "#deep-cuts-ccrep"}},
			wantMain:  "#deep-cuts",
			wantCcrep: "#deep-cuts-ccrep",
		},
	}
	for _, tc := range cases {
		main, ccrep := famconfig.FamChannels(tc.reg)
		if main != tc.wantMain || ccrep != tc.wantCcrep {
			t.Errorf("%s: FamChannels() = (%q, %q), want (%q, %q)",
				tc.name, main, ccrep, tc.wantMain, tc.wantCcrep)
		}
	}
}

func TestFamLedgerDirName(t *testing.T) {
	cases := []struct {
		reg  Registry
		want string
	}{
		{Registry{}, "botfam-collab"},
		{Registry{Name: "botfam"}, "botfam-collab"},
		{Registry{Name: "deep-cuts"}, "deep-cuts-collab"},
		{Registry{Name: "deep-cuts", Slug: "dc"}, "dc-collab"},
	}
	for _, tc := range cases {
		if got := famconfig.FamLedgerDirName(tc.reg); got != tc.want {
			t.Errorf("FamLedgerDirName(%+v) = %q, want %q", tc.reg, got, tc.want)
		}
	}
}

func TestDefaultHistoryPath(t *testing.T) {
	root := t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	wt := filepath.Join(root, "wt-agy")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, wt)

	// No matching config stanza: legacy ledger directory under the fam dir.
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	got, err := famconfig.DefaultHistoryPath(wt)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "botfam-collab", "history.jsonl")
	if got != want {
		t.Errorf("no registry: DefaultHistoryPath = %q, want %q", got, want)
	}

	// With a [repo.deep-cuts] stanza at root: per-fam ledger directory.
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{"deep-cuts": {Path: root}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = famconfig.DefaultHistoryPath(wt)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(root, "deep-cuts-collab", "history.jsonl")
	if got != want {
		t.Errorf("named registry: DefaultHistoryPath = %q, want %q", got, want)
	}
}

func TestLoadFamRegistryFromConfig(t *testing.T) {
	root := t.TempDir()
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	wt := filepath.Join(root, "wt-agy")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, wt)
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{"deep-cuts": {Path: root, Slug: "dc"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := famconfig.LoadFamRegistry(wt)
	if got.Name != "deep-cuts" {
		t.Errorf("Name = %q, want deep-cuts", got.Name)
	}
	// Channels are derived from the slug (no explicit channels in config).
	main, ccrep := famconfig.FamChannels(got)
	if main != "#dc" || ccrep != "#dc-ccrep" {
		t.Errorf("channels = (%q,%q), want (#dc,#dc-ccrep)", main, ccrep)
	}
}

func TestDefaultPassFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	passDir := filepath.Join(home, ".botfam")
	if err := os.MkdirAll(passDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePass := func(name string) string {
		t.Helper()
		path := filepath.Join(passDir, name)
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// Neither file exists: anonymous connect.
	if got := famconfig.DefaultPassFile("deep-cuts", "claude"); got != "" {
		t.Errorf("no pass files: got %q, want empty", got)
	}

	// Legacy file only: fall back to it.
	legacy := writePass("irc-pass-claude")
	if got := famconfig.DefaultPassFile("deep-cuts", "claude"); got != legacy {
		t.Errorf("legacy only: got %q, want %q", got, legacy)
	}

	// Fam-scoped file wins over legacy.
	scoped := writePass("irc-pass-deep-cuts-claude")
	if got := famconfig.DefaultPassFile("deep-cuts", "claude"); got != scoped {
		t.Errorf("fam-scoped present: got %q, want %q", got, scoped)
	}

	// No fam name: only the legacy candidate is tried.
	if got := famconfig.DefaultPassFile("", "claude"); got != legacy {
		t.Errorf("no fam name: got %q, want %q", got, legacy)
	}

	// Slug-scoped file (e.g. irc-pass-dc-agy) resolves via the slug.
	dcScoped := writePass("irc-pass-dc-claude")
	if got := famconfig.DefaultPassFile("dc", "claude"); got != dcScoped {
		t.Errorf("slug-scoped: got %q, want %q", got, dcScoped)
	}
}

func TestDefaultPassFilePrefersActorSlug(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	passDir := filepath.Join(home, ".botfam")
	if err := os.MkdirAll(passDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string) string {
		t.Helper()
		path := filepath.Join(passDir, name)
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// Both orderings present: the going-forward actor-slug ordering wins (#137).
	write("irc-pass-botfam-claude") // legacy slug-actor
	actorSlug := write("irc-pass-claude-botfam")
	if got := famconfig.DefaultPassFile("botfam", "claude"); got != actorSlug {
		t.Errorf("both orderings: got %q, want actor-slug %q", got, actorSlug)
	}
}

func TestFamScopedNick(t *testing.T) {
	cases := []struct {
		actor, slug, want string
	}{
		{"claude", "botfam", "claude-botfam"},
		{"agy", "dc", "agy-dc"},
		{"claude", "", "claude"},                     // no slug: bare actor
		{"", "botfam", ""},                           // no actor: empty
		{"claude-botfam", "botfam", "claude-botfam"}, // idempotent
		{"agy-dc", "dc", "agy-dc"},                   // idempotent
	}
	for _, tc := range cases {
		if got := famconfig.FamScopedNick(tc.actor, tc.slug); got != tc.want {
			t.Errorf("FamScopedNick(%q, %q) = %q, want %q", tc.actor, tc.slug, got, tc.want)
		}
	}
}

func TestFamSlugAndBranchFallback(t *testing.T) {
	// Slug present wins; absent falls back to the name.
	if got := famconfig.FamSlug(Registry{Name: "deep-cuts", Slug: "dc"}); got != "dc" {
		t.Errorf("FamSlug = %q, want dc", got)
	}
	if got := famconfig.FamSlug(Registry{Name: "deep-cuts"}); got != "deep-cuts" {
		t.Errorf("FamSlug fallback = %q, want deep-cuts", got)
	}
	// Branch: explicit integration_branch wins; absent derives <slug>-next.
	if got := famconfig.FamBranch(Registry{Name: "deep-cuts", IntegrationBranch: "dc-next"}); got != "dc-next" {
		t.Errorf("FamBranch = %q, want dc-next", got)
	}
	if got := famconfig.FamBranch(Registry{Name: "deep-cuts"}); got != "deep-cuts-next" {
		t.Errorf("FamBranch fallback = %q, want deep-cuts-next", got)
	}
}
