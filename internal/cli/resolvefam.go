package cli

import (
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
)

// ResolvedFam re-exports famconfig.ResolvedFam (the canonical fam identity), now
// owned by the dependency-free leaf so forge can import it too (#231).
type ResolvedFam = famconfig.ResolvedFam

// ResolveFam delegates to famctx.ResolveAgentRuntime — the single, fail-closed fam
// identity resolver. See famconfig.ResolveFam for the exact refusal modes.
// Callers that legitimately run outside an agent worktree (doctor/setup/whoami/
// version) must not gate on this.
func ResolveFam(workDir string) (ResolvedFam, error) {
	c, err := famctx.ResolveAgentRuntime(workDir)
	if err != nil {
		return ResolvedFam{}, err
	}
	return ResolvedFam{
		FamIdentity: famconfig.FamIdentity{
			FamDir:      c.FamDir,
			FamTOMLPath: c.FamTOMLPath,
			Name:        c.Name,
			Actor:       c.Actor,
			ActorRole:   c.ActorRole,
			Source:      c.Source,
		},
		Slug:         c.Slug,
		WorktreeRoot: c.WorktreeRoot,
		ForgeURL:     c.Registry.ForgeURL,
		Repository:   c.Registry.Repository,
		TokenPath:    c.TokenPath,
		Agent:        c.Agent,
		Registry:     c.Registry,
		Flags:        c.Flags,
	}, nil
}
