package fam

import (
	"errors"
	"os"
	"path/filepath"
)

// Legacy defaults used when no fam registry is resolvable. They match the
// original single-fam botfam deployment so existing setups keep working
// until their fam.toml gains explicit entries.
const (
	legacyMainChannel  = "#botfam"
	legacyCcrepChannel = "#ccrep"
	legacyLedgerDir    = "botfam-collab"
)

// LoadFamRegistry resolves the fam root for workDir and reads its fam.toml.
// It never fails: when no root or registry is resolvable it returns a zero
// Registry, which makes every derivation below fall back to the legacy
// botfam defaults.
func LoadFamRegistry(workDir string) Registry {
	info, err := (Resolver{WorkDir: workDir}).Resolve()
	if err != nil || info.Root == "" {
		return Registry{}
	}
	reg, err := ReadRegistry(filepath.Join(info.Root, "fam.toml"))
	if err != nil {
		return Registry{}
	}
	return reg
}

// FamSlug returns the short identifier used in derived channel names,
// ledger directories, and pass-file names: the explicit fam.toml slug when
// set, else the fam name.
func FamSlug(reg Registry) string {
	if reg.Slug != "" {
		return reg.Slug
	}
	return reg.Name
}

// FamChannels returns the fam's main and ccrep IRC channels. Explicit
// fam.toml channels win (first entry is main, second is ccrep); missing
// entries derive from the fam slug (#<slug> and #<slug>-ccrep); with no
// registry at all the legacy literals (#botfam, #ccrep) apply.
func FamChannels(reg Registry) (main, ccrep string) {
	if len(reg.Channels) > 0 {
		main = reg.Channels[0]
	}
	if len(reg.Channels) > 1 {
		ccrep = reg.Channels[1]
	}
	slug := FamSlug(reg)
	if main == "" {
		if slug != "" {
			main = "#" + slug
		} else {
			main = legacyMainChannel
		}
	}
	if ccrep == "" {
		if slug != "" {
			ccrep = "#" + slug + "-ccrep"
		} else {
			ccrep = legacyCcrepChannel
		}
	}
	return main, ccrep
}

// FamLedgerDirName returns the per-fam collab ledger directory name
// (<slug>-collab), falling back to the legacy botfam-collab when the
// registry has no slug or name.
func FamLedgerDirName(reg Registry) string {
	if slug := FamSlug(reg); slug != "" {
		return slug + "-collab"
	}
	return legacyLedgerDir
}

// DefaultHistoryPath returns the fam's history ledger path
// (<root>/<name>-collab/history.jsonl) for workDir. It fails when no fam
// root can be resolved; a missing or unreadable fam.toml only drops the
// name derivation back to the legacy ledger directory.
func DefaultHistoryPath(workDir string) (string, error) {
	info, err := (Resolver{WorkDir: workDir}).Resolve()
	if err != nil || info.Root == "" {
		return "", errors.New("family root could not be resolved")
	}
	reg, err := ReadRegistry(filepath.Join(info.Root, "fam.toml"))
	if err != nil {
		reg = Registry{}
	}
	return filepath.Join(info.Root, FamLedgerDirName(reg), "history.jsonl"), nil
}

// DefaultPassFile returns the IRC pass file to use for nick when --pass-file
// is omitted: ~/.botfam/irc-pass-<slug>-<nick> when present, else the legacy
// ~/.botfam/irc-pass-<nick> when present, else "" (anonymous connect).
func DefaultPassFile(famSlug, nick string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	var candidates []string
	if famSlug != "" {
		candidates = append(candidates, filepath.Join(home, ".botfam", "irc-pass-"+famSlug+"-"+nick))
	}
	candidates = append(candidates, filepath.Join(home, ".botfam", "irc-pass-"+nick))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
