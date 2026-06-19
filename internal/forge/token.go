package forge

import "github.com/robertolupi/botfam/internal/famconfig"

// HarnessTokenPath is a forwarding alias to famconfig.HarnessTokenPath, kept so
// existing forge.HarnessTokenPath callers keep working after the canonical
// implementation moved to the dependency-free leaf (#231).
func HarnessTokenPath(harness string) (string, error) {
	return famconfig.HarnessTokenPath(harness)
}
