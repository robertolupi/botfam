package fam

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderClaudeMCP(t *testing.T) {
	wt := t.TempDir()
	if err := RenderClaudeMCP(wt, "http://gitea.home.rlupi.com:3000/", "/fams/dc/.botfam/token-claude"); err != nil {
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
	forge, ok := cfg.MCPServers["forge"]
	if !ok {
		t.Fatalf("no forge server; got %+v", cfg.MCPServers)
	}
	if forge.Env["GITEA_ACCESS_TOKEN_FILE"] != "/fams/dc/.botfam/token-claude" {
		t.Errorf("token file = %q", forge.Env["GITEA_ACCESS_TOKEN_FILE"])
	}
	if len(forge.Args) < 4 || forge.Args[3] != "http://gitea.home.rlupi.com:3000/" {
		t.Errorf("forge args = %v", forge.Args)
	}
	home, _ := os.UserHomeDir()
	wantForge := filepath.Join(home, "bin", "gitea-mcp-server")
	if forge.Command != wantForge {
		t.Errorf("forge command = %q, want vendored %q", forge.Command, wantForge)
	}
	wantBotfam := filepath.Join(home, "bin", "botfam")
	if bf, ok := cfg.MCPServers["botfam"]; !ok || bf.Command != wantBotfam {
		t.Errorf("botfam server = %+v ok=%v, want command %q", bf, ok, wantBotfam)
	}
}

func TestRenderClaudeMCPRequiresForgeURL(t *testing.T) {
	if err := RenderClaudeMCP(t.TempDir(), "", "/x/token"); err == nil {
		t.Fatal("expected error for empty forge_url")
	}
}

func TestRenderGitIdentity(t *testing.T) {
	wt := t.TempDir()
	gitInit(t, wt) // from resolvefam_test.go

	if err := RenderGitIdentity(wt, "claude", "roberto.lupi+claude@gmail.com"); err != nil {
		t.Fatalf("RenderGitIdentity: %v", err)
	}
	name, _ := gitOne(wt, "config", "--worktree", "user.name")
	email, _ := gitOne(wt, "config", "--worktree", "user.email")
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
	if _, err := gitOutput(wt, "config", "user.email", "roberto.lupi@gmail.com"); err != nil {
		t.Fatal(err)
	}
	if err := RenderGitIdentity(wt, "agy", ""); err != nil {
		t.Fatalf("RenderGitIdentity: %v", err)
	}
	email, _ := gitOne(wt, "config", "--worktree", "user.email")
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
