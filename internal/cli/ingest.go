package cli

import (
	"time"

	"github.com/robertolupi/botfam/internal/ingest"
)

// The spool ingester now lives in the dependency-free internal/ingest leaf
// (#311). cli keeps this thin adapter over the leaf for its command builders
// (internal/mcp calls the leaf directly).

// Poller re-exports ingest.Poller.
type Poller = ingest.Poller

// Ingester re-exports ingest.Ingester.
type Ingester = ingest.Ingester

// ForgeClient re-exports ingest.ForgeClient.
type ForgeClient = ingest.ForgeClient

// NewIngester re-exports ingest.NewIngester.
func NewIngester(spoolDir string, interval time.Duration, pollers ...Poller) *Ingester {
	return ingest.NewIngester(spoolDir, interval, pollers...)
}

// NewIRCPoller re-exports ingest.NewIRCPoller.
func NewIRCPoller(logPath, matchNick string) Poller { return ingest.NewIRCPoller(logPath, matchNick) }

// NewForgePoller re-exports ingest.NewForgePoller.
func NewForgePoller(client ForgeClient, repo string) Poller {
	return ingest.NewForgePoller(client, repo)
}

// ForgePollerFor re-exports ingest.ForgePollerFor.
func ForgePollerFor(workDir, actor string) (Poller, error) {
	return ingest.ForgePollerFor(workDir, actor)
}

// IngestParams re-exports ingest.IngestParams.
func IngestParams(workDir string) (spoolDir, ircLogPath, matchNick string, err error) {
	return ingest.IngestParams(workDir)
}
