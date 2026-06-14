package forge

import (
	"fmt"
	"os"
	"path/filepath"
)

// HarnessTokenPath returns the per-harness token path ~/.botfam/token-<harness>.
func HarnessTokenPath(harness string) (string, error) {
	if harness == "" {
		return "", fmt.Errorf("harness is empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".botfam", "token-"+harness), nil
}
