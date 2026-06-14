package fam

import (
	"runtime/debug"
)

// Version is the current semantic version of botfam.
const Version = "0.1.0"

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
		var vcsTime string
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
			if setting.Key == "vcs.modified" && setting.Value == "true" {
				modified = true
			}
			if setting.Key == "vcs.time" {
				vcsTime = setting.Value
			}
		}
		if revision != "" {
			short := revision
			if len(short) > 7 {
				short = short[:7]
			}
			if modified {
				short += "-dirty"
			}
			date := ""
			if len(vcsTime) >= 10 {
				date = ", " + vcsTime[:10]
			}
			return Version + " (" + short + date + ")"
		}
	}

	return "dev"
}
