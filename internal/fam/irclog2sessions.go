package fam

// Port of the retired tools/irclog2sessions.py. Output is kept byte-identical
// to the Python version (including the generated-by header) so reruns over old
// logs never churn the committed session docs.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const chatLogSep = " : "

// ircEvent is one client->server channel event extracted from ergo's chat.log.
type ircEvent struct {
	ts      time.Time
	channel string // lowercased
	nick    string
	kind    string // message | notice | action | join | part | topic
	text    string
}

// parseChatLogLine splits one chat.log line into (ts, nick, raw IRC command).
// Only userinput records match: server->client traffic (useroutput) contains
// CHATHISTORY replays and services notices, and client->server lines addressed
// to NickServ/PASS never match a channel target, so credentials cannot leak
// into the rendered transcripts.
func parseChatLogLine(line string) (ts time.Time, nick, raw string, ok bool, err error) {
	parts := strings.SplitN(strings.TrimRight(line, "\n"), chatLogSep, 7)
	if len(parts) != 7 || strings.TrimSpace(parts[2]) != "userinput" {
		return time.Time{}, "", "", false, nil
	}
	ts, err = time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return time.Time{}, "", "", false, err
	}
	return ts.UTC(), parts[4], parts[6], true, nil
}

// parseIRCCommand maps a raw client command to (channel, kind, text), or
// ok=false if the command is not channel-bound.
func parseIRCCommand(raw string) (channel, kind, text string, ok bool) {
	tokens := strings.SplitN(raw, " ", 3)
	cmd := strings.ToUpper(tokens[0])
	if (cmd == "PRIVMSG" || cmd == "NOTICE") && len(tokens) == 3 {
		target := tokens[1]
		if !strings.HasPrefix(target, "#") {
			return "", "", "", false
		}
		text := strings.TrimPrefix(tokens[2], ":")
		if action, found := strings.CutPrefix(text, "\x01ACTION "); found {
			return target, "action", strings.TrimRight(action, "\x01"), true
		}
		if cmd == "NOTICE" {
			return target, "notice", text, true
		}
		return target, "message", text, true
	}
	if (cmd == "JOIN" || cmd == "PART") && len(tokens) >= 2 {
		ch, _, _ := strings.Cut(tokens[1], ",")
		if strings.HasPrefix(ch, "#") {
			return ch, strings.ToLower(cmd), "", true
		}
	}
	// Setting a topic (a bare "TOPIC #chan" query has no payload).
	if cmd == "TOPIC" && len(tokens) == 3 && strings.HasPrefix(tokens[2], ":") {
		if strings.HasPrefix(tokens[1], "#") {
			return tokens[1], "topic", tokens[2][1:], true
		}
	}
	return "", "", "", false
}

// replaceInvalidUTF8 mirrors Python's errors="replace" file decoding: every
// invalid byte becomes U+FFFD, so rendered output stays valid UTF-8.
func replaceInvalidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// readChatLogs parses the given chat.log files into channel events, keeping
// only the requested channels (nil means all), sorted by timestamp.
func readChatLogs(paths []string, channels map[string]bool) ([]ircEvent, error) {
	var events []ircEvent
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			ts, nick, raw, ok, err := parseChatLogLine(replaceInvalidUTF8(sc.Text()))
			if err != nil {
				f.Close()
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if !ok {
				continue
			}
			channel, kind, text, ok := parseIRCCommand(raw)
			if !ok {
				continue
			}
			channel = strings.ToLower(channel)
			if channels != nil && !channels[channel] {
				continue
			}
			events = append(events, ircEvent{ts, channel, nick, kind, text})
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].ts.Before(events[j].ts) })
	return events, nil
}

// sessionize splits a single channel's event list on silence gaps and topic
// changes. Setting the channel topic deliberately starts a new, titled
// session — that's the fam convention for marking what a stretch of work is
// about.
func sessionize(events []ircEvent, gap time.Duration) [][]ircEvent {
	var sessions [][]ircEvent
	for _, e := range events {
		last := len(sessions) - 1
		boundary := last < 0 ||
			e.ts.Sub(sessions[last][len(sessions[last])-1].ts) > gap ||
			(e.kind == "topic" && e.text != "")
		if boundary {
			sessions = append(sessions, nil)
			last++
		}
		sessions[last] = append(sessions[last], e)
	}
	return sessions
}

var nonSlugRe = regexp.MustCompile("[^a-z0-9]+")

func slugify(text string) string {
	const maxLen = 48
	slug := strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if len(slug) > maxLen {
		slug = slug[:maxLen]
	}
	return strings.TrimRight(slug, "-")
}

// sessionTitle returns the topic text if the session opened with a topic
// change, else "".
func sessionTitle(session []ircEvent) string {
	if session[0].kind == "topic" && session[0].text != "" {
		return session[0].text
	}
	return ""
}

func sessionDirname(channel string, session []ircEvent, loc *time.Location, taken map[string]bool) string {
	start := session[0].ts.In(loc)
	chanSuffix := ""
	if channel != "#botfam" {
		chanSuffix = "-" + strings.TrimLeft(channel, "#")
	}
	name := ""
	if slug := slugify(sessionTitle(session)); slug != "" {
		name = start.Format("2006-01-02") + chanSuffix + "-" + slug
	} else {
		name = start.Format("2006-01-02") + "-irc" + chanSuffix + "-" + start.Format("1504")
	}
	if taken[name] { // same slug twice in one day: disambiguate by start time
		name += "-" + start.Format("1504")
	}
	taken[name] = true
	return name
}

func renderSession(channel string, session []ircEvent, loc *time.Location) string {
	start, end := session[0].ts.In(loc), session[len(session)-1].ts.In(loc)
	seen := map[string]bool{}
	var participants []string
	for _, e := range session {
		if !seen[e.nick] {
			seen[e.nick] = true
			participants = append(participants, e.nick)
		}
	}
	sort.Slice(participants, func(i, j int) bool {
		li, lj := strings.ToLower(participants[i]), strings.ToLower(participants[j])
		if li != lj {
			return li < lj
		}
		return participants[i] < participants[j]
	})

	tzName := start.Format("MST")
	span := fmt.Sprintf("%s %s–%s %s",
		start.Format("2006-01-02"), start.Format("15:04"), end.Format("15:04"), tzName)
	var heading string
	if title := sessionTitle(session); title != "" {
		heading = fmt.Sprintf("# %s (%s, %s)", title, channel, span)
	} else {
		heading = fmt.Sprintf("# IRC session: %s — %s", channel, span)
	}

	var comment string
	if tzName == "UTC" {
		comment = "<!-- GENERATED by tools/irclog2sessions.py from ergo chat.log -->"
	} else {
		comment = fmt.Sprintf("<!-- GENERATED by tools/irclog2sessions.py from ergo chat.log (timezone: %s) -->", tzName)
	}

	lines := []string{
		comment,
		heading,
		"",
		"Participants: " + strings.Join(participants, ", "),
		"",
		"---",
		"",
	}
	for _, e := range session {
		stamp := e.ts.In(loc).Format("15:04:05")
		switch e.kind {
		case "message":
			lines = append(lines, fmt.Sprintf("- **%s** %s: %s", stamp, e.nick, e.text))
		case "notice":
			lines = append(lines, fmt.Sprintf("- **%s** %s (notice): %s", stamp, e.nick, e.text))
		case "action":
			lines = append(lines, fmt.Sprintf("- **%s** *%s %s*", stamp, e.nick, e.text))
		case "topic":
			lines = append(lines, fmt.Sprintf("- *%s %s set topic: %s*", stamp, e.nick, e.text))
		default: // join/part
			lines = append(lines, fmt.Sprintf("- *%s %s %sed*", stamp, e.nick, e.kind))
		}
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func isSessionEmpty(session []ircEvent) bool {
	for _, e := range session {
		if e.kind == "message" || e.kind == "notice" || e.kind == "action" {
			return false
		}
	}
	return true
}

// IrcLog2SessionsCmd parses ergo chat.log file(s) into per-channel session
// transcripts under --out, one markdown file per session:
//
//	OUT/YYYY-MM-DD-<topic-slug>/session.md   (topic-titled)
//	OUT/YYYY-MM-DD-irc-HHMM/session.md       (untitled fallback)
//
// Sessions split on silence gaps (--gap-minutes) and on topic changes; the
// topic text titles the new session. Names are deterministic from the log, so
// reruns are idempotent. The trailing, possibly still-running session of each
// channel is skipped unless --include-open is given.
func IrcLog2SessionsCmd(args []string, out io.Writer) error {
	outDir := "doc/collab/sessions"
	gapMinutes := 30.0
	includeOpen := false
	timezoneStr := "UTC"
	var logs, channelArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--out="):
			outDir = strings.TrimPrefix(arg, "--out=")
		case arg == "--out":
			i++
			if i < len(args) {
				outDir = args[i]
			}
		case strings.HasPrefix(arg, "--gap-minutes="):
			v, err := strconv.ParseFloat(strings.TrimPrefix(arg, "--gap-minutes="), 64)
			if err != nil {
				return fmt.Errorf("invalid --gap-minutes: %w", err)
			}
			gapMinutes = v
		case arg == "--gap-minutes":
			i++
			if i < len(args) {
				v, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid --gap-minutes: %w", err)
				}
				gapMinutes = v
			}
		case strings.HasPrefix(arg, "--channel="):
			channelArgs = append(channelArgs, strings.TrimPrefix(arg, "--channel="))
		case arg == "--channel":
			i++
			if i < len(args) {
				channelArgs = append(channelArgs, args[i])
			}
		case strings.HasPrefix(arg, "--timezone="):
			timezoneStr = strings.TrimPrefix(arg, "--timezone=")
		case arg == "--timezone":
			i++
			if i < len(args) {
				timezoneStr = args[i]
			}
		case arg == "--include-open":
			includeOpen = true
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown irclog2sessions argument %q", arg)
		default:
			logs = append(logs, arg)
		}
	}
	if len(logs) == 0 {
		return errors.New("usage: botfam irclog2sessions CHATLOG [CHATLOG...] [--out <dir>] [--gap-minutes <n>] [--channel <chan>]... [--include-open] [--timezone <zone>]")
	}

	var loc *time.Location
	switch strings.ToLower(timezoneStr) {
	case "local":
		loc = time.Local
	case "utc":
		loc = time.UTC
	default:
		var err error
		loc, err = time.LoadLocation(timezoneStr)
		if err != nil {
			return fmt.Errorf("invalid --timezone %q: %w", timezoneStr, err)
		}
	}

	var channels map[string]bool
	if len(channelArgs) > 0 {
		channels = make(map[string]bool, len(channelArgs))
		for _, c := range channelArgs {
			channels[strings.ToLower(c)] = true
		}
	}
	gap := time.Duration(gapMinutes * float64(time.Minute))

	events, err := readChatLogs(logs, channels)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return errors.New("no channel events found (is chat.log capturing userinput?)")
	}
	newest := events[len(events)-1].ts

	chanSet := map[string]bool{}
	for _, e := range events {
		chanSet[e.channel] = true
	}
	chans := make([]string, 0, len(chanSet))
	for c := range chanSet {
		chans = append(chans, c)
	}
	sort.Strings(chans)

	written, skippedOpen := 0, 0
	taken := map[string]bool{}
	for _, channel := range chans {
		var chanEvents []ircEvent
		for _, e := range events {
			if e.channel == channel {
				chanEvents = append(chanEvents, e)
			}
		}
		for _, session := range sessionize(chanEvents, gap) {
			if isSessionEmpty(session) {
				continue
			}
			if !includeOpen && newest.Sub(session[len(session)-1].ts) <= gap {
				skippedOpen++
				continue
			}
			dir := filepath.Join(outDir, sessionDirname(channel, session, loc, taken))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(dir, "session.md")
			if err := os.WriteFile(path, []byte(renderSession(channel, session, loc)), 0o644); err != nil {
				return err
			}
			written++
			fmt.Fprintf(out, "wrote %s (%d events)\n", path, len(session))
		}
	}
	if skippedOpen > 0 {
		fmt.Fprintf(out, "skipped %d possibly-open session(s); use --include-open to render anyway\n", skippedOpen)
	}
	if written == 0 && skippedOpen == 0 {
		fmt.Fprintln(out, "nothing to write")
	}
	return nil
}
