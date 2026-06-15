package fam

import (
	"time"

	"github.com/robertolupi/botfam/internal/irc"
)

// The reusable IRC log domain now lives in the dependency-free internal/irc leaf
// (#311). internal/fam re-exports it so the irc-client/irc-wait/scribe command
// builders (still here until phase 3) and internal/mcp compile unchanged.

// HistoryEntry re-exports irc.HistoryEntry.
type HistoryEntry = irc.HistoryEntry

// WaitIrcLines re-exports irc.WaitIrcLines.
func WaitIrcLines(logPath, nick string, fromOffset int64, timeout time.Duration) (lines []string, newOffset int64, timedOut bool, err error) {
	return irc.WaitIrcLines(logPath, nick, fromOffset, timeout)
}

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
