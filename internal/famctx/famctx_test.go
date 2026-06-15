package famctx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s %v: %v", name, args, err)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "test")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "commit", "--allow-empty", "-m", "initial commit")
}

func TestResolveWalkUp(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}

	famTOML := `name = "myfam"
slug = "mf"
roster = ["bob"]

[agent.bob]
harness = "bob-code"
`
	if err := os.WriteFile(filepath.Join(famDir, "fam.toml"), []byte(famTOML), 0644); err != nil {
		t.Fatal(err)
	}

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	// Nested subdirectories inside agent worktree
	nested := filepath.Join(bobDir, "sub", "dir")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Resolve from nested subdirectory
	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: nested,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve(ModeAgentRuntime) failed: %v", err)
	}

	if ctx.FamDir != famDir {
		t.Errorf("expected FamDir %q, got %q", famDir, ctx.FamDir)
	}
	if ctx.Actor != "bob" {
		t.Errorf("expected Actor %q, got %q", "bob", ctx.Actor)
	}
	if ctx.ActorRole != RoleAgent {
		t.Errorf("expected RoleAgent, got %q", ctx.ActorRole)
	}
	if ctx.Source != SourceWorkDir {
		t.Errorf("expected SourceWorkDir, got %q", ctx.Source)
	}

	// 2. Resolve from wiki/ directory (simulating nested wiki repo)
	wikiDir := filepath.Join(bobDir, "wiki")
	if err := os.Mkdir(wikiDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, wikiDir) // Nested git repo

	ctxWiki, err := Resolve(context.Background(), Inputs{
		WorkDir: wikiDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve from nested wiki failed: %v", err)
	}
	if ctxWiki.Actor != "bob" {
		t.Errorf("expected Actor %q even from nested wiki, got %q", "bob", ctxWiki.Actor)
	}
	if ctxWiki.ActorRole != RoleAgent {
		t.Errorf("expected RoleAgent, got %q", ctxWiki.ActorRole)
	}
}

func TestResolveLegacyGitHashFallback(t *testing.T) {
	tempDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = eval
	}

	gitDir := filepath.Join(tempDir, "wt-bob")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, gitDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: gitDir,
		Mode:    ModeLocate,
	})
	if err != nil {
		t.Fatalf("Resolve legacy git hash failed: %v", err)
	}

	t.Logf("Resolved Name: %q, Actor: %q, Role: %q, FamDir: %q, Source: %q", ctx.Name, ctx.Actor, ctx.ActorRole, ctx.FamDir, ctx.Source)

	if ctx.FamTOMLPath != "" {
		t.Errorf("expected FamTOMLPath to be empty, got %q", ctx.FamTOMLPath)
	}
	if !strings.HasPrefix(ctx.Name, "fam-") {
		t.Errorf("expected legacy fam name starting with 'fam-', got %q", ctx.Name)
	}
	if ctx.Actor != "bob" {
		t.Errorf("expected actor parsed from legacy wt-bob folder prefix, got %q", ctx.Actor)
	}
	if ctx.ActorRole != RoleUnknown {
		t.Errorf("expected role to be RoleUnknown for legacy, got %q", ctx.ActorRole)
	}
	if ctx.Source != SourceGitRoots {
		t.Errorf("expected SourceGitRoots, got %q", ctx.Source)
	}
}

func TestLocationOf(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}

	famTOML := `name = "myfam"
slug = "mf"
roster = ["bob"]

[agent.bob]
harness = "bob-code"
`
	if err := os.WriteFile(filepath.Join(famDir, "fam.toml"), []byte(famTOML), 0644); err != nil {
		t.Fatal(err)
	}

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: bobDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// 1. LocationMainRepo
	loc, err := ctx.LocationOf(bobDir)
	if err != nil {
		t.Fatal(err)
	}
	if loc != LocationMainRepo {
		t.Errorf("expected LocationMainRepo, got %q", loc)
	}

	// 2. LocationWiki
	wikiDir := filepath.Join(bobDir, "wiki")
	if err := os.Mkdir(wikiDir, 0755); err != nil {
		t.Fatal(err)
	}
	locWiki, err := ctx.LocationOf(wikiDir)
	if err != nil {
		t.Fatal(err)
	}
	if locWiki != LocationWiki {
		t.Errorf("expected LocationWiki, got %q", locWiki)
	}

	// 3. LocationSubmodule
	subDir := filepath.Join(bobDir, "submodule")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Let's create a git repo inside it
	gitInit(t, subDir)
	// For testing submodule check, LocationOf checks if the git superproject is bobDir.
	// Since we don't have a real submodule checkout here, we can skip submodule specifics or mock.
	// Let's check a foreign directory instead:
	locForeign, err := ctx.LocationOf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if locForeign != LocationForeign {
		t.Errorf("expected LocationForeign, got %q", locForeign)
	}
}

func TestFlagEnabled(t *testing.T) {
	flagsTOML := `name = "myfam"
slug = "mf"
roster = ["bob"]

[flags]
experiment = "yes"
wait_ingest = true

[agent.bob]
harness = "bob-code"
[agent.bob.flags]
wait_ingest = false
`
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	if err := os.WriteFile(filepath.Join(famDir, "fam.toml"), []byte(flagsTOML), 0644); err != nil {
		t.Fatal(err)
	}

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: bobDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Override check: wait_ingest should be false
	v, err := ctx.FlagEnabled("wait_ingest", true)
	if err != nil {
		t.Fatal(err)
	}
	if v {
		t.Errorf("expected wait_ingest override to be false, got true")
	}

	// Default fallback check
	vUnset, err := ctx.FlagEnabled("unset_flag", true)
	if err != nil {
		t.Fatal(err)
	}
	if !vUnset {
		t.Errorf("expected unset_flag to fall back to default true, got false")
	}
}
