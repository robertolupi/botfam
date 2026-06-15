package cli

import "github.com/robertolupi/botfam/internal/famconfig"

// The fam identity/derivation helpers now live in the dependency-free famconfig
// leaf (#311). cli keeps these thin adapters over the leaf for its command builders;
// internal/mcp calls famconfig directly.

// LoadFamRegistry re-exports famconfig.LoadFamRegistry.
func LoadFamRegistry(workDir string) Registry { return famconfig.LoadFamRegistry(workDir) }

// FamSlug re-exports famconfig.FamSlug.
func FamSlug(reg Registry) string { return famconfig.FamSlug(reg) }

// FamBranch re-exports famconfig.FamBranch.
func FamBranch(reg Registry) string { return famconfig.FamBranch(reg) }

// FamChannels re-exports famconfig.FamChannels.
func FamChannels(reg Registry) (main, ccrep string) { return famconfig.FamChannels(reg) }

// FamLedgerDirName re-exports famconfig.FamLedgerDirName.
func FamLedgerDirName(reg Registry) string { return famconfig.FamLedgerDirName(reg) }

// DefaultHistoryPath re-exports famconfig.DefaultHistoryPath.
func DefaultHistoryPath(workDir string) (string, error) { return famconfig.DefaultHistoryPath(workDir) }

// DefaultPassFile re-exports famconfig.DefaultPassFile.
func DefaultPassFile(famSlug, actor string) string { return famconfig.DefaultPassFile(famSlug, actor) }

// FamScopedNick re-exports famconfig.FamScopedNick.
func FamScopedNick(actor, famSlug string) string { return famconfig.FamScopedNick(actor, famSlug) }
