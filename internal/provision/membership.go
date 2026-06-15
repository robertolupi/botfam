package provision

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// EnsureMembership verifies that workDir's repository belongs to the fam rooted
// at root. An explicit root (COLLAB_ROOT) is trusted and only ensured to exist.
// Otherwise the fam.toml must be readable and one of the repo's git object
// stores must be registered in object_stores — else membership is refused.
func EnsureMembership(root string, explicit bool, workDir string) error {
	if explicit {
		return os.MkdirAll(root, 0o755)
	}
	reg, err := famconfig.ReadRegistry(filepath.Join(root, "fam.toml"))
	if err != nil {
		return fmt.Errorf("fam root %s is not set up or readable; run botfam setup", root)
	}
	stores, err := famconfig.GitObjectStores(workDir)
	if err != nil {
		return err
	}
	if hasAny(reg.ObjectStores, stores) {
		return nil
	}
	return fmt.Errorf("repo object store is not registered for fam root %s; refusing unverified membership", root)
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
