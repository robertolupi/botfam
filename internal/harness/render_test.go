package harness

import (
	"encoding/json"
	"github.com/robertolupi/botfam/internal/gitexec"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderClaudeMCP(t *testing.T) {
	wt := t.TempDir()
	if err := RenderClaudeMCP(wt); err != nil {
		t.Fatalf("RenderClaudeMCP: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("rendered .mcp.json is not valid JSON: %v", err)
	}
	// The separate forge server is retired — its tools are now in-process
	// botfam subtools (#429).
	if _, ok := cfg.MCPServers["forge"]; ok {
		t.Errorf("forge server should no longer be rendered; got %+v", cfg.MCPServers)
	}
	home, _ := os.UserHomeDir()
	wantBotfam := filepath.Join(home, "bin", "botfam")
	if bf, ok := cfg.MCPServers["botfam"]; !ok || bf.Command != wantBotfam {
		t.Errorf("botfam server = %+v ok=%v, want command %q", bf, ok, wantBotfam)
	}

	// gopls is registered iff installed, at its resolved absolute path.
	gopls, ok := cfg.MCPServers["gopls"]
	if wantGopls := lookGopls(); wantGopls != "" {
		if !ok {
			t.Errorf("gopls is installed (%s) but not registered; got %+v", wantGopls, cfg.MCPServers)
		} else if gopls.Command != wantGopls || len(gopls.Args) != 1 || gopls.Args[0] != "mcp" {
			t.Errorf("gopls server = %+v, want command %q args [mcp]", gopls, wantGopls)
		}
	} else if ok {
		t.Errorf("gopls not installed but registered: %+v", gopls)
	}
}

// TestRenderClaudeMCPMigratesLegacyForge verifies a pre-existing standalone
// forge entry (from before #429) is removed on re-render, while unrelated
// servers are preserved.
func TestRenderClaudeMCPMigratesLegacyForge(t *testing.T) {
	wt := t.TempDir()
	seed := `{"mcpServers":{"forge":{"command":"/old/gitea-mcp-server","args":["-t","stdio"]},"custom":{"command":"/x/custom"}}}`
	if err := os.WriteFile(filepath.Join(wt, ".mcp.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderClaudeMCP(wt); err != nil {
		t.Fatalf("RenderClaudeMCP: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg mcpConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := cfg.MCPServers["forge"]; ok {
		t.Errorf("legacy forge entry should have been removed; got %+v", cfg.MCPServers)
	}
	if _, ok := cfg.MCPServers["custom"]; !ok {
		t.Errorf("unrelated server 'custom' should be preserved; got %+v", cfg.MCPServers)
	}
	if _, ok := cfg.MCPServers["botfam"]; !ok {
		t.Errorf("botfam server missing after render; got %+v", cfg.MCPServers)
	}
}

func TestRenderGitIdentity(t *testing.T) {
	wt := t.TempDir()
	gitInit(t, wt) // from resolvefam_test.go

	if err := RenderGitIdentity(wt, "claude", "roberto.lupi+claude@gmail.com"); err != nil {
		t.Fatalf("RenderGitIdentity: %v", err)
	}
	name, _ := gitexec.One(wt, "config", "--worktree", "user.name")
	email, _ := gitexec.One(wt, "config", "--worktree", "user.email")
	if name != "claude" {
		t.Errorf("user.name = %q", name)
	}
	if email != "roberto.lupi+claude@gmail.com" {
		t.Errorf("user.email = %q", email)
	}
}

func TestRenderGitIdentityEmailDefault(t *testing.T) {
	wt := t.TempDir()
	gitInit(t, wt)
	if _, err := gitexec.Output(wt, "config", "user.email", "roberto.lupi@gmail.com"); err != nil {
		t.Fatal(err)
	}
	if err := RenderGitIdentity(wt, "agy", ""); err != nil {
		t.Fatalf("RenderGitIdentity: %v", err)
	}
	email, _ := gitexec.One(wt, "config", "--worktree", "user.email")
	if email != "roberto.lupi+agy@gmail.com" {
		t.Errorf("plus-addressed email = %q, want roberto.lupi+agy@gmail.com", email)
	}
}

func TestPlusAddress(t *testing.T) {
	cases := map[string]string{
		"roberto.lupi@gmail.com": "roberto.lupi+claude@gmail.com",
		"noatsign":               "noatsign",
		"":                       "",
	}
	for in, want := range cases {
		if got := plusAddress(in, "claude"); got != want {
			t.Errorf("plusAddress(%q) = %q, want %q", in, got, want)
		}
	}
}
