package cli

import (
	"bufio"
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

	"github.com/spf13/cobra"
)

// IrcClientCmd is the thin args/io entry point retained for tests; it builds
// the Cobra command and runs it against args.
func IrcClientCmd(args []string, out io.Writer) error {
	return runCobra(NewIrcClientCmd(), args, out)
}

// NewIrcClientCmd builds the `botfam irc-client` Cobra command (FIFO-driven
// IRC client).
func NewIrcClientCmd() *cobra.Command {
	mainChannel, ccrepChannel := FamChannels(LoadFamRegistry("."))
	server := "localhost:6667"
	channel := mainChannel + "," + ccrepChannel
	var workDir, passFile string
	var rawNick bool
	c := &cobra.Command{
		Use:           "irc-client <actor>",
		Short:         "Run the FIFO-driven IRC client",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIrcClient(args[0], server, channel, workDir, passFile, rawNick, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&server, "server", server, "IRC server host:port")
	c.Flags().StringVar(&channel, "channel", channel, "comma-separated channels to join")
	c.Flags().StringVar(&workDir, "dir", "", "FIFO/log working dir (default scratch/irc/<actor>)")
	c.Flags().StringVar(&passFile, "pass-file", "", "NickServ pass file (default ~/.botfam/irc-pass-<actor>-<fam>)")
	c.Flags().BoolVar(&rawNick, "raw-nick", false, "use <actor> verbatim as the IRC nick instead of the fam-scoped <actor>-<fam>")
	return c
}

// emitter serializes timestamped log/stdout writes so the FIFO-reader and
// socket-reader goroutines in runIrcClient cannot interleave or corrupt lines.
type emitter struct {
	mu      sync.Mutex
	logFile io.Writer
	out     io.Writer
	now     func() time.Time // overridable in tests; defaults to time.Now
}

func (e *emitter) emit(line string) {
	now := e.now
	if now == nil {
		now = time.Now
	}
	formatted := fmt.Sprintf("[%s] %s\n", now().Format("15:04:05"), line)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.logFile != nil {
		_, _ = io.WriteString(e.logFile, formatted)
	}
	if e.out != nil {
		_, _ = io.WriteString(e.out, formatted)
	}
}

func runIrcClient(actor, server, channel, workDir, passFile string, rawNick bool, out io.Writer) error {
	famReg := LoadFamRegistry(".")
	mainChannel, _ := FamChannels(famReg)

	// The on-server identity is fam-scoped (claude-botfam, agy-dc) so agents
	// from different fams that share an actor name — and even the same wt-<actor>
	// dir — never collide on a shared IRC server (#137). --raw-nick opts out.
	// The bare actor still keys the FIFO dir (scratch/irc/<actor>) and pass-file.
	ircNick := actor
	if !rawNick {
		ircNick = FamScopedNick(actor, FamSlug(famReg))
	}

	if passFile == "" {
		passFile = DefaultPassFile(FamSlug(famReg), actor)
	}

	channelList, primaryChannel := ParseChannels(channel, mainChannel)

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
		workDir = filepath.Join("scratch", "irc", actor)
	}

	// Create directories
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	pidPath := filepath.Join(workDir, "pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("failed to write pidfile: %w", err)
	}
	defer os.Remove(pidPath)

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

	// emitHelper is called from both the FIFO-reader goroutine and the
	// socket-reader loop, so the emitter serializes its writes to logFile/out.
	// Without that lock the two writers race and can interleave/corrupt log
	// lines — and the log is the durable wake signal that irc-wait/ReadIrcLog
	// parse line-by-line. (conn.Write is separately safe per net.Conn.)
	em := &emitter{logFile: logFile, out: out}
	emitHelper := em.emit

	emitHelper(fmt.Sprintf("* connecting to %s as %s, channel %s", server, ircNick, channel))
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
			_, _ = fmt.Fprintf(conn, "PASS %s:%s\r\n", ircNick, password)
		}
	}

	_, _ = fmt.Fprintf(conn, "NICK %s\r\n", ircNick)
	_, _ = fmt.Fprintf(conn, "USER %s 0 * :%s\r\n", ircNick, ircNick)
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
				emitHelper(fmt.Sprintf("%s <%s> %s", target, ircNick, body))
			} else {
				emitHelper(fmt.Sprintf("(pm->%s) <%s> %s", target, ircNick, body))
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
				emitHelper(fmt.Sprintf("%s <%s> %s", target, ircNick, chunk))
			} else {
				emitHelper(fmt.Sprintf("(pm->%s) <%s> %s", target, ircNick, chunk))
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
				replyBody := fmt.Sprintf("[%s] version: %s", ircNick, GetVersion())
				_ = sendPrivmsg(replyTarget, replyBody)
			}
			continue
		}

		if m := eventRe.FindStringSubmatch(line); m != nil {
			src := m[1]
			evType := m[2]
			target := m[3]
			emitHelper(fmt.Sprintf("* %s %s %s", src, evType, target))
			if evType == "JOIN" && src == ircNick {
				announcement := fmt.Sprintf("[%s] version %s joined.", ircNick, GetVersion())
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

// ParseChannels splits a comma-separated list of channels and returns the
// normalized list and primary channel. An empty list falls back to fallback
// (the fam's main channel).
// ParseChannels moved to the internal/irc leaf (#311); re-exported in irc.go.
