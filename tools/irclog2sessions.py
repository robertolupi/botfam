#!/usr/bin/env python3
"""Parse ergo chat.log into per-channel session transcripts under doc/.

Reads ergo's raw-traffic log (the `chat.log` target: types userinput/useroutput
at debug level), keeps only client->server lines (userinput) addressed to
channels, splits them into sessions on silence gaps, and renders one markdown
transcript per session:

    OUT/YYYY-MM-DD-<topic-slug>/session.md        (topic-titled)
    OUT/YYYY-MM-DD-irc-HHMM/session.md            (untitled fallback)

Titling convention: setting the channel topic (/topic what we're working on)
both STARTS a new session and titles it. Sessions also split on silence gaps.
Untitled sessions fall back to the irc-HHMM name; renaming those directories
by hand is fine — names are deterministic from the log, so reruns are
idempotent and a rename just leaves the old name unused.

Only userinput is parsed, by design: server->client traffic (useroutput)
contains CHATHISTORY replays (duplicates) and services notices, and
client->server lines addressed to NickServ/PASS never match a channel
target, so credentials cannot leak into the rendered transcripts.

Usage:
    tools/irclog2sessions.py CHATLOG [CHATLOG...]
        [--out doc/collab/sessions] [--gap-minutes 30]
        [--channel '#botfam' ...] [--include-open]
"""

import argparse
import datetime as dt
import os
import re
import sys

LOG_SEP = " : "
ACTION_RE = re.compile("^\x01ACTION (.*)\x01?$")


def parse_args():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("logs", nargs="+", help="ergo chat.log file(s)")
    p.add_argument("--out", default="doc/collab/sessions",
                   help="output directory (default: doc/collab/sessions)")
    p.add_argument("--gap-minutes", type=float, default=30,
                   help="silence gap that starts a new session (default: 30)")
    p.add_argument("--channel", action="append",
                   help="only these channels (repeatable; default: all)")
    p.add_argument("--include-open", action="store_true",
                   help="also render the trailing session even if it may "
                        "still be running (within one gap of the last entry)")
    return p.parse_args()


def parse_log_line(line):
    """One chat.log line -> (ts, nick, raw_irc_line) for userinput, else None."""
    parts = line.rstrip("\n").split(LOG_SEP, 6)
    if len(parts) != 7 or parts[2].strip() != "userinput":
        return None
    ts = dt.datetime.strptime(parts[0], "%Y-%m-%dT%H:%M:%S.%fZ")
    return ts, parts[4], parts[6]


def parse_irc(nick, raw):
    """Raw client command -> (channel, kind, text) or None if not channel-bound."""
    tokens = raw.split(" ", 2)
    cmd = tokens[0].upper()
    if cmd in ("PRIVMSG", "NOTICE") and len(tokens) == 3:
        target = tokens[1]
        if not target.startswith("#"):
            return None
        text = tokens[2][1:] if tokens[2].startswith(":") else tokens[2]
        m = ACTION_RE.match(text)
        if m:
            return target, "action", m.group(1).rstrip("\x01")
        return target, "notice" if cmd == "NOTICE" else "message", text
    if cmd in ("JOIN", "PART") and len(tokens) >= 2:
        chan = tokens[1].split(" ")[0].split(",")[0]
        if chan.startswith("#"):
            return chan, cmd.lower(), ""
    if cmd == "TOPIC" and len(tokens) == 3 and tokens[2].startswith(":"):
        # setting a topic (a bare "TOPIC #chan" query has no payload)
        chan = tokens[1]
        if chan.startswith("#"):
            return chan, "topic", tokens[2][1:]
    return None


def read_events(paths, channels):
    events = []  # (ts, channel, nick, kind, text)
    for path in paths:
        with open(path, encoding="utf-8", errors="replace") as f:
            for line in f:
                parsed = parse_log_line(line)
                if not parsed:
                    continue
                ts, nick, raw = parsed
                irc = parse_irc(nick, raw)
                if not irc:
                    continue
                channel, kind, text = irc
                if channels and channel.lower() not in channels:
                    continue
                events.append((ts, channel.lower(), nick, kind, text))
    events.sort(key=lambda e: e[0])
    return events


def sessionize(events, gap):
    """Split a single channel's event list on silence gaps and topic changes.

    Setting the channel topic deliberately starts a new, titled session —
    that's the fam convention for marking what a stretch of work is about.
    """
    sessions = []
    for event in events:
        boundary = (not sessions
                    or event[0] - sessions[-1][-1][0] > gap
                    or (event[3] == "topic" and event[4]))
        if boundary:
            sessions.append([])
        sessions[-1].append(event)
    return sessions


def slugify(text, max_len=48):
    slug = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    return slug[:max_len].rstrip("-")


def session_title(session):
    """Topic text if the session opened with a topic change, else None."""
    first = session[0]
    if first[3] == "topic" and first[4]:
        return first[4]
    return None


def session_dirname(channel, session, taken):
    start = session[0][0]
    chan = "" if channel == "#botfam" else f"-{channel.lstrip('#')}"
    title = session_title(session)
    slug = slugify(title) if title else None
    name = (f"{start:%Y-%m-%d}{chan}-{slug}" if slug
            else f"{start:%Y-%m-%d}-irc{chan}-{start:%H%M}")
    if name in taken:  # same slug twice in one day: disambiguate by start time
        name = f"{name}-{start:%H%M}"
    taken.add(name)
    return name


def render(channel, session):
    start, end = session[0][0], session[-1][0]
    participants = sorted({nick for _, _, nick, _, _ in session},
                          key=str.lower)
    title = session_title(session)
    heading = (f"# {title} ({channel}, {start:%Y-%m-%d} "
               f"{start:%H:%M}–{end:%H:%M} UTC)" if title else
               f"# IRC session: {channel} — {start:%Y-%m-%d} "
               f"{start:%H:%M}–{end:%H:%M} UTC")
    lines = [
        "<!-- GENERATED by tools/irclog2sessions.py from ergo chat.log -->",
        heading,
        "",
        f"Participants: {', '.join(participants)}",
        "",
        "---",
        "",
    ]
    for ts, _, nick, kind, text in session:
        stamp = f"{ts:%H:%M:%S}"
        if kind == "message":
            lines.append(f"- **{stamp}** {nick}: {text}")
        elif kind == "notice":
            lines.append(f"- **{stamp}** {nick} (notice): {text}")
        elif kind == "action":
            lines.append(f"- **{stamp}** *{nick} {text}*")
        elif kind == "topic":
            lines.append(f"- *{stamp} {nick} set topic: {text}*")
        else:  # join/part
            lines.append(f"- *{stamp} {nick} {kind}ed*")
    lines.append("")
    return "\n".join(lines)


def main():
    args = parse_args()
    gap = dt.timedelta(minutes=args.gap_minutes)
    channels = {c.lower() for c in args.channel} if args.channel else None
    events = read_events(args.logs, channels)
    if not events:
        sys.exit("no channel events found (is chat.log capturing userinput?)")
    newest = events[-1][0]

    written = skipped_open = 0
    taken = set()
    for channel in sorted({e[1] for e in events}):
        chan_events = [e for e in events if e[1] == channel]
        for session in sessionize(chan_events, gap):
            if not args.include_open and newest - session[-1][0] <= gap:
                skipped_open += 1
                continue
            dirname = os.path.join(args.out,
                                   session_dirname(channel, session, taken))
            os.makedirs(dirname, exist_ok=True)
            path = os.path.join(dirname, "session.md")
            with open(path, "w", encoding="utf-8") as f:
                f.write(render(channel, session))
            written += 1
            print(f"wrote {path} ({len(session)} events)")
    if skipped_open:
        print(f"skipped {skipped_open} possibly-open session(s); "
              "use --include-open to render anyway")
    if not written and not skipped_open:
        print("nothing to write")


if __name__ == "__main__":
    main()
