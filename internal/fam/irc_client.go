package fam

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// IrcClientCmd executes the Go-based FIFO-driven IRC client.
func IrcClientCmd(args []string, out io.Writer) error {
	var nick, server, channel, workDir, passFile string
	server = "localhost:6667"
	channel = "#botfam,#ccrep"

	// Parse arguments
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--server="):
			server = strings.TrimPrefix(arg, "--server=")
		case arg == "--server":
			i++
			if i < len(args) {
				server = args[i]
			}
		case strings.HasPrefix(arg, "--channel="):
			channel = strings.TrimPrefix(arg, "--channel=")
		case arg == "--channel":
			i++
			if i < len(args) {
				channel = args[i]
			}
		case strings.HasPrefix(arg, "--dir="):
			workDir = strings.TrimPrefix(arg, "--dir=")
		case arg == "--dir":
			i++
			if i < len(args) {
				workDir = args[i]
			}
		case strings.HasPrefix(arg, "--pass-file="):
			passFile = strings.TrimPrefix(arg, "--pass-file=")
		case arg == "--pass-file":
			i++
			if i < len(args) {
				passFile = args[i]
			}
		default:
			if !strings.HasPrefix(arg, "-") {
				cleanArgs = append(cleanArgs, arg)
			} else {
				return fmt.Errorf("unknown argument %q", arg)
			}
		}
	}

	if len(cleanArgs) < 1 {
		return errors.New("missing required nickname argument: botfam irc-client <nick>")
	}
	nick = cleanArgs[0]

	channelList, primaryChannel := ParseChannels(channel)

	var joinedMu sync.RWMutex
	joinedChannels := make(map[string]bool)
	for _, ch := range channelList {
		joinedChannels[ch] = true
	}

	isJoined := func(target string) bool {
		joinedMu.RLock()
		defer joinedMu.RUnlock()
		return joinedChannels[target]
	}

	addJoined := func(target string) {
		joinedMu.Lock()
		defer joinedMu.Unlock()
		joinedChannels[target] = true
	}

	if workDir == "" {
		workDir = filepath.Join("scratch", "irc", nick)
	}

	// Create directories
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	fifoPath := filepath.Join(workDir, "in")
	logPath := filepath.Join(workDir, "log")

	// Create FIFO if it doesn't exist
	if _, err := os.Stat(fifoPath); os.IsNotExist(err) {
		if err := syscall.Mkfifo(fifoPath, 0666); err != nil {
			return fmt.Errorf("failed to create FIFO: %w", err)
		}
	}

	// Open FIFO in read-write mode to prevent EOF churn when writer exits
	fifoFile, err := os.OpenFile(fifoPath, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open FIFO: %w", err)
	}
	defer fifoFile.Close()

	// Open log file in append mode
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer logFile.Close()

	emitHelper := func(line string) {
		stamp := time.Now().Format("15:04:05")
		formatted := fmt.Sprintf("[%s] %s\n", stamp, line)
		_, _ = logFile.WriteString(formatted)
		_, _ = fmt.Fprint(out, formatted)
	}

	emitHelper(fmt.Sprintf("* connecting to %s as %s, channel %s", server, nick, channel))
	emitHelper(fmt.Sprintf("* send: write lines to %s", fifoPath))

	// Connect to IRC server
	conn, err := net.DialTimeout("tcp", server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()

	// Send initial commands
	if passFile != "" {
		passBytes, err := os.ReadFile(passFile)
		if err != nil {
			return fmt.Errorf("failed to read password file: %w", err)
		}
		password := strings.TrimSpace(string(passBytes))
		fields := strings.Fields(password)
		if len(fields) > 0 {
			password = fields[len(fields)-1]
		}
		if password != "" {
			_, _ = fmt.Fprintf(conn, "PASS %s:%s\r\n", nick, password)
		}
	}

	_, _ = fmt.Fprintf(conn, "NICK %s\r\n", nick)
	_, _ = fmt.Fprintf(conn, "USER %s 0 * :%s\r\n", nick, nick)
	_, _ = fmt.Fprintf(conn, "JOIN %s\r\n", strings.Join(channelList, ","))

	privRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+PRIVMSG\s+(\S+)\s+:(.*)$`)
	eventRe := regexp.MustCompile(`^:([^!\s]+)\S*\s+(JOIN|PART|QUIT|NICK)\b\s*:?(\S*)`)

	// Channel to signal shutdown
	done := make(chan struct{})

	sendPrivmsg := func(target string, body string) error {
		maxLen := 400 // Conservative chunk size to stay safely under 512-byte IRC limit
		if len(body) <= maxLen {
			cmd := fmt.Sprintf("PRIVMSG %s :%s\r\n", target, body)
			if _, err := conn.Write([]byte(cmd)); err != nil {
				return err
			}
			if isJoined(target) {
				emitHelper(fmt.Sprintf("%s <%s> %s", target, nick, body))
			} else {
				emitHelper(fmt.Sprintf("(pm->%s) <%s> %s", target, nick, body))
			}
			return nil
		}

		remaining := body
		for len(remaining) > 0 {
			chunk := remaining
			if len(chunk) > maxLen {
				chunk = remaining[:maxLen]
				if idx := strings.LastIndex(chunk, " "); idx > 0 {
					chunk = remaining[:idx]
				}
			}
			cmd := fmt.Sprintf("PRIVMSG %s :%s\r\n", target, chunk)
			if _, err := conn.Write([]byte(cmd)); err != nil {
				return err
			}
			if isJoined(target) {
				emitHelper(fmt.Sprintf("%s <%s> %s", target, nick, chunk))
			} else {
				emitHelper(fmt.Sprintf("(pm->%s) <%s> %s", target, nick, chunk))
			}
			remaining = strings.TrimSpace(remaining[len(chunk):])
			time.Sleep(100 * time.Millisecond) // flood control
		}
		return nil
	}

	// Goroutine to read from FIFO and send to socket
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(fifoFile)
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}

			var err error
			if strings.HasPrefix(text, "/raw ") {
				cmd := strings.TrimPrefix(text, "/raw ") + "\r\n"
				_, err = conn.Write([]byte(cmd))
			} else if strings.HasPrefix(text, "/join ") {
				targetChans := strings.TrimSpace(strings.TrimPrefix(text, "/join "))
				if targetChans != "" {
					for _, tc := range strings.Split(targetChans, ",") {
						tc = strings.TrimSpace(tc)
						if tc != "" {
							addJoined(tc)
						}
					}
					cmd := fmt.Sprintf("JOIN %s\r\n", targetChans)
					_, err = conn.Write([]byte(cmd))
				}
			} else if strings.HasPrefix(text, "/msg ") {
				parts := strings.SplitN(strings.TrimPrefix(text, "/msg "), " ", 2)
				if len(parts) < 2 {
					emitHelper("* usage: /msg NICK text")
					continue
				}
				target := parts[0]
				body := parts[1]
				err = sendPrivmsg(target, body)
			} else {
				err = sendPrivmsg(primaryChannel, text)
			}

			if err != nil {
				emitHelper(fmt.Sprintf("* send error: %v", err))
				return
			}
		}
	}()

	// Read from socket in main thread
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			emitHelper("* server closed connection")
			break
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "PING") {
			pong := "PONG" + strings.TrimPrefix(line, "PING") + "\r\n"
			_, _ = conn.Write([]byte(pong))
			continue
		}

		if m := privRe.FindStringSubmatch(line); m != nil {
			src := m[1]
			target := m[2]
			text := m[3]
			where := "(pm)"
			if strings.HasPrefix(target, "#") {
				where = target
			}
			emitHelper(fmt.Sprintf("%s <%s> %s", where, src, text))
			if strings.HasPrefix(strings.TrimSpace(text), "!version") {
				replyTarget := target
				if !strings.HasPrefix(replyTarget, "#") {
					replyTarget = src
				}
				replyBody := fmt.Sprintf("[%s] version: %s", nick, GetVersion())
				_ = sendPrivmsg(replyTarget, replyBody)
			}
			continue
		}

		if m := eventRe.FindStringSubmatch(line); m != nil {
			src := m[1]
			evType := m[2]
			target := m[3]
			emitHelper(fmt.Sprintf("* %s %s %s", src, evType, target))
			if evType == "JOIN" && src == nick {
				announcement := fmt.Sprintf("[%s] version %s joined.", nick, GetVersion())
				_ = sendPrivmsg(target, announcement)
			}
			continue
		}

		// Print raw line for numeric replies or other formats
		emitHelper(fmt.Sprintf(". %s", line))
	}

	<-done
	return nil
}

// ParseChannels splits a comma-separated list of channels and returns the normalized list and primary channel.
func ParseChannels(channelStr string) (channels []string, primary string) {
	for _, ch := range strings.Split(channelStr, ",") {
		ch = strings.TrimSpace(ch)
		if ch != "" {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		channels = []string{"#botfam"}
	}
	return channels, channels[0]
}
