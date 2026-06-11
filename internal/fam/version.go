package fam

import (
	"runtime/debug"
)

// BuildSHA is injected at compile time via -ldflags.
// If not injected, it defaults to "dev".
var BuildSHA = "dev"

// GetVersion returns the compiled BuildSHA if it is not "dev".
// Otherwise, it attempts to read the Go build info's VCS revision.
// If that fails, it returns "dev".
func GetVersion() string {
	if BuildSHA != "dev" {
		return BuildSHA
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		var modified bool
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
			}
		}
		if revision != "" {
			if modified {
				return revision + "-dirty"
			}
			return revision
		}
	}

	return "dev"
}
