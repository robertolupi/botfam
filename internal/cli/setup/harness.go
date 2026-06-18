package setup

import "github.com/robertolupi/botfam/internal/harness"

// The agent-harness MCP-config editors and the per-worktree renderers now live
// in the dependency-free internal/harness leaf (#311). cli keeps these thin
// adapters over the leaf for its setup/clone command builders.

// MCPConfigurator re-exports harness.MCPConfigurator.
type MCPConfigurator = harness.MCPConfigurator

// MCPServerSpec re-exports harness.MCPServerSpec.
type MCPServerSpec = harness.MCPServerSpec

// Scope re-exports harness.Scope.
type Scope = harness.Scope

// Scope constants re-exported from the harness leaf.
const (
	Project = harness.Project
	Global  = harness.Global
)

// NewClaudeMCPConfigurator re-exports harness.NewClaudeMCPConfigurator.
func NewClaudeMCPConfigurator(worktree string) *harness.ClaudeMCPConfigurator {
	return harness.NewClaudeMCPConfigurator(worktree)
}

// NewCodexMCPConfigurator re-exports harness.NewCodexMCPConfigurator.
func NewCodexMCPConfigurator() *harness.CodexMCPConfigurator {
	return harness.NewCodexMCPConfigurator()
}

// NewAntigravityMCPConfigurator re-exports harness.NewAntigravityMCPConfigurator.
func NewAntigravityMCPConfigurator() *harness.AntigravityMCPConfigurator {
	return harness.NewAntigravityMCPConfigurator()
}

// RenderClaudeMCP re-exports harness.RenderClaudeMCP.
func RenderClaudeMCP(worktree, forgeURL, tokenPath string) error {
	return harness.RenderClaudeMCP(worktree, forgeURL, tokenPath)
}

// RenderGitIdentity re-exports harness.RenderGitIdentity.
func RenderGitIdentity(worktree, name, email string) error {
	return harness.RenderGitIdentity(worktree, name, email)
}
