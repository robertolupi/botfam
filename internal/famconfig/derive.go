package famconfig

import (
	"errors"
	"path/filepath"
)

// Legacy defaults used when no fam registry is resolvable. They match the
// original single-fam botfam deployment so existing setups keep working
// until their fam.toml gains explicit entries.
const (
	legacyLedgerDir = "botfam-collab"
)

// LoadFamRegistry resolves the merged Registry for workDir from the global
// config. It never fails: when no `[repo.<k>]` stanza matches it returns a zero
// Registry, which makes every derivation below fall back to the legacy botfam
// defaults.
func LoadFamRegistry(workDir string) Registry {
	reg, err := ResolveConfig(workDir)
	if err != nil {
		return Registry{}
	}
	return reg
}

// FamBranch returns the integration branch for this family: bots open PRs here.
// Priority: explicit integration_branch > legacy branch > <slug>-next > botfam-next.
func FamBranch(reg Registry) string {
	if reg.IntegrationBranch != "" {
		return reg.IntegrationBranch
	}
	if reg.Branch != "" {
		return reg.Branch
	}
	slug := FamSlug(reg)
	if slug != "" {
		return slug + "-next"
	}
	return "botfam-next"
}

// FamReleaseBranch returns the public release branch (default: main).
// Bots must never target this branch with PRs unless explicitly instructed.
func FamReleaseBranch(reg Registry) string {
	if reg.ReleaseBranch != "" {
		return reg.ReleaseBranch
	}
	return "main"
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
	info, err := (GitResolver{}).ResolveIdentity(workDir)
	if err != nil || info.FamDir == "" {
		return "", errors.New("family root could not be resolved")
	}
	reg := LoadFamRegistry(workDir)
	return filepath.Join(info.FamDir, FamLedgerDirName(reg), "history.jsonl"), nil
}
