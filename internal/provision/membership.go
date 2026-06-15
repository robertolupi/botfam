package provision

import (
	"fmt"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// EnsureMembership verifies that workDir's repository belongs to the fam.
// The fam.toml must be readable and one of the repo's git object
// stores must be registered in object_stores — else membership is refused.
func EnsureMembership(id famconfig.FamIdentity, workDir string) error {
	if id.FamTOMLPath == "" {
		return fmt.Errorf("no fam.toml resolved; refusing unverified membership")
	}
	reg, err := famconfig.ReadRegistry(id.FamTOMLPath)
	if err != nil {
		return fmt.Errorf("fam root %s is not set up or readable; run botfam setup", id.FamDir)
	}
	stores, err := famconfig.GitObjectStores(workDir)
	if err != nil {
		return err
	}
	if hasAny(reg.ObjectStores, stores) {
		return nil
	}
	return fmt.Errorf("repo object store is not registered for fam root %s; refusing unverified membership", id.FamDir)
}

func hasAny(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, y := range b {
		if set[y] {
			return true
		}
	}
	return false
}
