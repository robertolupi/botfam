package fam

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IrcProposeCmd is a one-shot CCREP propose: it joins the channel under a
// "-cli" suffixed nick (so it never collides with the actor's registered,
// strictly-enforced interactive nick), sends a single !propose line, and
// disconnects. Built for operators driving CCREP from a shell or an
// Obsidian hotkey without keeping a client running.
func IrcProposeCmd(args []string, out io.Writer) error {
	var id, sha, quorum, deadline, executor, summary, as string
	server := "localhost:6667"
	channel, _ := FamChannels(LoadFamRegistry("."))
	quorum = "majority"

	for i := 0; i < len(args); i++ {
		arg := args[i]
		consume := func(name string) (string, bool) {
			if strings.HasPrefix(arg, "--"+name+"=") {
				return strings.TrimPrefix(arg, "--"+name+"="), true
			}
			if arg == "--"+name && i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		if v, ok := consume("id"); ok {
			id = v
		} else if v, ok := consume("sha"); ok {
			sha = v
		} else if v, ok := consume("quorum"); ok {
			quorum = v
		} else if v, ok := consume("deadline"); ok {
			deadline = v
		} else if v, ok := consume("executor"); ok {
			executor = v
		} else if v, ok := consume("summary"); ok {
			summary = v
		} else if v, ok := consume("as"); ok {
			as = v
		} else if v, ok := consume("server"); ok {
			server = v
		} else if v, ok := consume("channel"); ok {
			channel = v
		} else {
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	if id == "" || summary == "" {
		return fmt.Errorf("usage: botfam irc-propose --id <id> --summary <text> [--sha <sha>] [--quorum all|majority|any] [--deadline <RFC3339>] [--executor <actor>] [--as <actor>] [--server <host:port>] [--channel <chan>]")
	}
	switch quorum {
	case "all", "majority", "any":
	default:
		return fmt.Errorf("invalid quorum %q (all|majority|any)", quorum)
	}

	if sha == "" {
		head, err := exec.Command("git", "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("--sha omitted and git rev-parse HEAD failed: %w", err)
		}
		sha = strings.TrimSpace(string(head))
	}

	if as == "" {
		top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return fmt.Errorf("--as omitted and not in a git checkout: %w", err)
		}
		as = ParseActor(filepath.Base(strings.TrimSpace(string(top))), ResolveRepoName("."))
	}
	if executor == "" {
		executor = as
	}
	nick := as + "-cli"

	line := fmt.Sprintf("!propose id=%s sha=%s quorum=%s executor=%s", id, sha, quorum, executor)
	if deadline != "" {
		line += " deadline=" + deadline
	}
	line += fmt.Sprintf(" summary=%q", summary)
	// Bang commands must be a single PRIVMSG line for the scribe parser:
	// no splitting allowed, so reject instead.
	if len(line) > 400 {
		return fmt.Errorf("proposal line is %d bytes (max 400) — shorten the summary", len(line))
	}

	conn, err := net.DialTimeout("tcp", server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "NICK %s\r\n", nick)
	fmt.Fprintf(conn, "USER %s 0 * :%s one-shot propose\r\n", nick, as)

	registered := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			msg := scanner.Text()
			switch {
			case strings.HasPrefix(msg, "PING"):
				fmt.Fprintf(conn, "PONG%s\r\n", strings.TrimPrefix(msg, "PING"))
			case strings.Contains(msg, " 001 "):
				registered <- nil
				return
			case strings.Contains(msg, " 433 "), strings.Contains(msg, "NICKNAME_RESERVED"):
				registered <- fmt.Errorf("nick %q unavailable: %s", nick, msg)
				return
			}
		}
		registered <- fmt.Errorf("connection closed before registration")
	}()

	select {
	case err := <-registered:
		if err != nil {
			return err
		}
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timed out waiting for IRC registration")
	}

	fmt.Fprintf(conn, "JOIN %s\r\n", channel)
	fmt.Fprintf(conn, "PRIVMSG %s :%s\r\n", channel, line)
	// Give the server a moment to relay before quitting.
	time.Sleep(500 * time.Millisecond)
	fmt.Fprintf(conn, "QUIT :one-shot propose done\r\n")

	fmt.Fprintf(out, "sent to %s as %s:\n%s\n", channel, nick, line)
	return nil
}
