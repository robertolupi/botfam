package cli

import "github.com/robertolupi/botfam/internal/famconfig"

// ResolvedFam re-exports famconfig.ResolvedFam (the canonical fam identity), now
// owned by the dependency-free leaf so forge can import it too (#231).
type ResolvedFam = famconfig.ResolvedFam

// ResolveFam delegates to famconfig.ResolveFam — the single, fail-closed fam
// identity resolver. See famconfig.ResolveFam for the exact refusal modes.
// Callers that legitimately run outside an agent worktree (doctor/setup/whoami/
// version) must not gate on this.
func ResolveFam(workDir string) (ResolvedFam, error) {
	return famconfig.ResolveFam(workDir)
}
