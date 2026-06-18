package ops

import (
	"github.com/robertolupi/botfam/internal/irc"
)

// The reusable IRC log domain now lives in the dependency-free internal/irc leaf
// (#311). cli keeps these thin adapters over the leaf for the irc-client/scribe
// command builders (internal/mcp calls the leaf directly).

// HistoryEntry re-exports irc.HistoryEntry.
type HistoryEntry = irc.HistoryEntry

// ParseChannels re-exports irc.ParseChannels.
func ParseChannels(channelStr, fallback string) (channels []string, primary string) {
	return irc.ParseChannels(channelStr, fallback)
}
