package provision

import (
	"fmt"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// EnsureMembership verifies that workDir belongs to a registered fam: a
// `[repo.<k>]` stanza in ~/.botfam/config.toml whose path is an ancestor of
// workDir must resolve (#404). The path match replaces the old object-store
// check; ResolveConfig is the single matcher. The id argument is retained for
// call-site compatibility but membership is determined by workDir alone.
func EnsureMembership(_ famconfig.FamIdentity, workDir string) error {
	if _, err := famconfig.ResolveConfig(workDir); err != nil {
		return fmt.Errorf("%s is not a registered fam in ~/.botfam/config.toml; run botfam setup (%v)", workDir, err)
	}
	return nil
}
