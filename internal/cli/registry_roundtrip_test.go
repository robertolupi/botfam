package cli

import (
	"path/filepath"
	"testing"
)

// TestRegistryRoundTrip writes the new-schema Registry (forge_url, repository,
// [agent.<name>]/[user.<name>] tables) and reads it back, so WriteRegistry and
// ReadRegistry agree on the unified fam.toml format.
func TestRegistryRoundTrip(t *testing.T) {
	in := Registry{
		Name:       "deep-cuts",
		Slug:       "dc",
		ForgeURL:   "http://gitea.home.rlupi.com:3000/",
		Repository: "deep-cuts/deep-cuts",
		Roster:     []string{"claude", "agy", "rlupi"},
		Agents: map[string]AgentConfig{
			"claude": {Harness: "claude-code", ForgeUser: "claude-bot"},
			"agy":    {Harness: "antigravity", ForgeUser: "agy-bot", Email: "roberto.lupi+agy@gmail.com"},
		},
		Users: map[string]AgentConfig{
			"rlupi": {ForgeUser: "rlupi"},
		},
	}
	path := filepath.Join(t.TempDir(), "fam.toml")
	if err := WriteRegistry(path, in); err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	got, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if got.Name != in.Name || got.Slug != in.Slug || got.ForgeURL != in.ForgeURL || got.Repository != in.Repository {
		t.Errorf("scalars mismatch: %+v", got)
	}
	c, ok := got.Agents["claude"]
	if !ok || c.Harness != "claude-code" || c.ForgeUser != "claude-bot" || c.Name != "claude" || c.IsUser {
		t.Errorf("agent.claude round-trip = %+v ok=%v", c, ok)
	}
	if a := got.Agents["agy"]; a.Email != "roberto.lupi+agy@gmail.com" {
		t.Errorf("agent.agy email lost: %+v", a)
	}
	r, ok := got.Users["rlupi"]
	if !ok || !r.IsUser || r.ForgeUser != "rlupi" {
		t.Errorf("user.rlupi round-trip = %+v ok=%v", r, ok)
	}
	if _, isAgent := got.Agents["rlupi"]; isAgent {
		t.Errorf("rlupi leaked into agents")
	}
}
