package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		main, ccrep := FamChannels(tc.reg)
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
		if got := FamLedgerDirName(tc.reg); got != tc.want {
			t.Errorf("FamLedgerDirName(%+v) = %q, want %q", tc.reg, got, tc.want)
		}
	}
}

func TestDefaultHistoryPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COLLAB_ROOT", root)
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_FAM", "")

	// No fam.toml: legacy ledger directory.
	got, err := DefaultHistoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "botfam-collab", "history.jsonl")
	if got != want {
		t.Errorf("no registry: DefaultHistoryPath = %q, want %q", got, want)
	}

	// With a named fam.toml: per-fam ledger directory.
	reg := Registry{Name: "deep-cuts", CreatedAt: "2026-06-12T00:00:00Z"}
	if err := WriteRegistry(filepath.Join(root, "fam.toml"), reg); err != nil {
		t.Fatal(err)
	}
	got, err = DefaultHistoryPath(root)
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(root, "deep-cuts-collab", "history.jsonl")
	if got != want {
		t.Errorf("named registry: DefaultHistoryPath = %q, want %q", got, want)
	}
}

func TestLoadFamRegistryRoundTripsChannels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COLLAB_ROOT", root)
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_FAM", "")

	reg := Registry{
		Name:      "deep-cuts",
		Channels:  []string{"#deep-cuts", "#deep-cuts-ccrep"},
		CreatedAt: "2026-06-12T00:00:00Z",
	}
	if err := WriteRegistry(filepath.Join(root, "fam.toml"), reg); err != nil {
		t.Fatal(err)
	}
	got := LoadFamRegistry(root)
	if got.Name != "deep-cuts" {
		t.Errorf("Name = %q, want deep-cuts", got.Name)
	}
	if strings.Join(got.Channels, ",") != "#deep-cuts,#deep-cuts-ccrep" {
		t.Errorf("Channels = %v, want [#deep-cuts #deep-cuts-ccrep]", got.Channels)
	}

	// A fam.toml without channels must not grow a channels key on rewrite.
	if err := WriteRegistry(filepath.Join(root, "fam.toml"), Registry{Name: "deep-cuts"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "fam.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "channels") {
		t.Errorf("channel-less registry serialized a channels key:\n%s", data)
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
	if got := DefaultPassFile("deep-cuts", "claude"); got != "" {
		t.Errorf("no pass files: got %q, want empty", got)
	}

	// Legacy file only: fall back to it.
	legacy := writePass("irc-pass-claude")
	if got := DefaultPassFile("deep-cuts", "claude"); got != legacy {
		t.Errorf("legacy only: got %q, want %q", got, legacy)
	}

	// Fam-scoped file wins over legacy.
	scoped := writePass("irc-pass-deep-cuts-claude")
	if got := DefaultPassFile("deep-cuts", "claude"); got != scoped {
		t.Errorf("fam-scoped present: got %q, want %q", got, scoped)
	}

	// No fam name: only the legacy candidate is tried.
	if got := DefaultPassFile("", "claude"); got != legacy {
		t.Errorf("no fam name: got %q, want %q", got, legacy)
	}

	// Slug-scoped file (e.g. irc-pass-dc-agy) resolves via the slug.
	dcScoped := writePass("irc-pass-dc-claude")
	if got := DefaultPassFile("dc", "claude"); got != dcScoped {
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
	if got := DefaultPassFile("botfam", "claude"); got != actorSlug {
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
		if got := FamScopedNick(tc.actor, tc.slug); got != tc.want {
			t.Errorf("FamScopedNick(%q, %q) = %q, want %q", tc.actor, tc.slug, got, tc.want)
		}
	}
}

func TestRegistrySlugRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fam.toml")

	// Slug present: persists and parses back.
	if err := WriteRegistry(path, Registry{Name: "deep-cuts", Slug: "dc"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "dc" {
		t.Errorf("Slug = %q, want dc", got.Slug)
	}
	if FamSlug(got) != "dc" {
		t.Errorf("FamSlug = %q, want dc", FamSlug(got))
	}

	// No slug: key is omitted on write and FamSlug falls back to the name.
	if err := WriteRegistry(path, Registry{Name: "deep-cuts"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "slug") {
		t.Errorf("slug-less registry serialized a slug key:\n%s", data)
	}
	got, err = ReadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if FamSlug(got) != "deep-cuts" {
		t.Errorf("FamSlug fallback = %q, want deep-cuts", FamSlug(got))
	}
}

func TestRegistryBranchRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fam.toml")

	// Branch present: persists and parses back.
	if err := WriteRegistry(path, Registry{Name: "deep-cuts", Branch: "dc-next"}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "dc-next" {
		t.Errorf("Branch = %q, want dc-next", got.Branch)
	}
	if FamBranch(got) != "dc-next" {
		t.Errorf("FamBranch = %q, want dc-next", FamBranch(got))
	}

	// No branch: key is omitted on write and FamBranch falls back.
	if err := WriteRegistry(path, Registry{Name: "deep-cuts"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "branch") {
		t.Errorf("branch-less registry serialized a branch key:\n%s", data)
	}
	got, err = ReadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if FamBranch(got) != "deep-cuts-next" {
		t.Errorf("FamBranch fallback = %q, want deep-cuts-next", FamBranch(got))
	}
}
