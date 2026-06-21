package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
)

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
	if got.Slug != "dc" {
		t.Errorf("Slug = %q, want dc", got.Slug)
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
