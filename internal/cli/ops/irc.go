package ops

import (
	"github.com/robertolupi/botfam/internal/irc"
)

// The reusable IRC log domain now lives in the dependency-free internal/irc leaf
// (#311). cli keeps these thin adapters over the leaf for the irc-client/scribe
// command builders (internal/mcp calls the leaf directly).

// HistoryEntry re-exports irc.HistoryEntry.
type HistoryEntry = irc.HistoryEntry

// ReadIrcLog re-exports irc.ReadIrcLog.
func ReadIrcLog(logPath string, fromOffset int64, maxLines int) (lines []string, nextOffset int64, err error) {
	return irc.ReadIrcLog(logPath, fromOffset, maxLines)
}

// ReplayHistory re-exports irc.ReplayHistory.
func ReplayHistory(historyPath, actor, matchNick, since string, filterChans []string) (lines []string, nextOffset int64, err error) {
	return irc.ReplayHistory(historyPath, actor, matchNick, since, filterChans)
}

// ParseChannels re-exports irc.ParseChannels.
func ParseChannels(channelStr, fallback string) (channels []string, primary string) {
	return irc.ParseChannels(channelStr, fallback)
}
