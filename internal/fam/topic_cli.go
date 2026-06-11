package fam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rlupi/botfam/internal/store"
)

// TopicCmd dispatches topic CLI subcommands.
func TopicCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return printTopicHelp(out)
	}

	sub := args[0]
	switch sub {
	case "publish":
		return topicPublish(args[1:], out)
	case "listen":
		return topicListen(args[1:], out)
	case "cursor":
		return topicCursor(args[1:], out)
	case "-h", "--help", "help":
		return printTopicHelp(out)
	default:
		return fmt.Errorf("unknown topic command %q", sub)
	}
}

func printTopicHelp(out io.Writer) error {
	fmt.Fprint(out, `Usage:
  botfam topic publish --topic <name> --message <body>
  botfam topic listen --topic <name>
  botfam topic cursor --topic <name> [--agent <name>] [--update <id>]
`)
	return nil
}

func topicPublish(args []string, out io.Writer) error {
	var topic, message string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--topic=") {
			topic = strings.TrimPrefix(arg, "--topic=")
		} else if arg == "--topic" {
			i++
			if i < len(args) {
				topic = args[i]
			}
		} else if strings.HasPrefix(arg, "--message=") {
			message = strings.TrimPrefix(arg, "--message=")
		} else if arg == "--message" {
			i++
			if i < len(args) {
				message = args[i]
			}
		} else {
			return fmt.Errorf("unknown publish argument %q", arg)
		}
	}

	if topic == "" {
		return errors.New("missing required --topic <name>")
	}
	if message == "" {
		return errors.New("missing required --message <body>")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	reqPayload := map[string]any{
		"work_dir": info.Root,
		"actor":    actor,
		"topic":    topic,
		"body":     message,
	}

	var respMsg store.TopicMessage
	err = sendDaemonRequest(context.Background(), "topic_publish", reqPayload, &respMsg)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, respMsg)
	}

	fmt.Fprintf(out, "[%d] %s: %s\n", respMsg.ID, respMsg.From, respMsg.Body)
	return nil
}

func splitArgs(s string) []string {
	var args []string
	var current strings.Builder
	inDoubleQuotes := false
	inSingleQuotes := false
	escaped := false
	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingleQuotes {
			escaped = true
			continue
		}
		if r == '"' && !inSingleQuotes {
			inDoubleQuotes = !inDoubleQuotes
			continue
		}
		if r == '\'' && !inDoubleQuotes {
			inSingleQuotes = !inSingleQuotes
			continue
		}
		if (r == ' ' || r == '\t') && !inDoubleQuotes && !inSingleQuotes {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

func topicListen(args []string, out io.Writer) error {
	var topic string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--topic=") {
			topic = strings.TrimPrefix(arg, "--topic=")
		} else if arg == "--topic" {
			i++
			if i < len(args) {
				topic = args[i]
			}
		} else {
			return fmt.Errorf("unknown listen argument %q", arg)
		}
	}

	if topic == "" {
		return errors.New("missing required --topic <name>")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	udsPath, err := getSocketPath()
	if err != nil {
		return err
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(dialCtx, "unix", udsPath)
			},
		},
	}

	reqPayload := map[string]any{
		"work_dir": info.Root,
		"actor":    actor,
		"topic":    topic,
	}
	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", "http://localhost/topic_listen", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return errors.New(errResp.Error)
		}
		return fmt.Errorf("daemon endpoint returned status %s", resp.Status)
	}

	// Start stdin reader loop in background
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			if strings.HasPrefix(line, "/") {
				args := splitArgs(line[1:])
				if len(args) == 0 {
					continue
				}
				cmdName := args[0]
				cmdArgs := args[1:]

				var err error
				switch cmdName {
				case "setup":
					err = Setup(cmdArgs, out)
				case "session":
					err = SessionCmd(cmdArgs, out)
				case "topic":
					err = TopicCmd(cmdArgs, out)
				case "merge-gate":
					err = MergeGateCmd(cmdArgs, out)
				case "agent-docs":
					err = AgentDocsCmd(cmdArgs, out)
				case "vote":
					err = VoteCmd(cmdArgs, out)
				case "tally":
					err = TallyCmd(cmdArgs, out)
				case "propose":
					err = ProposeCmd(cmdArgs, out)
				case "approve":
					err = ApproveCmd(cmdArgs, out)
				case "merge":
					err = MergeCmd(cmdArgs, out)
				case "send":
					err = SendCmd(cmdArgs, out)
				case "recv":
					err = RecvCmd(cmdArgs, out)
				case "try-recv", "try_recv":
					err = TryRecvCmd(cmdArgs, out)
				case "peek":
					err = PeekCmd(cmdArgs, out)
				case "ack":
					err = AckCmd(cmdArgs, out)
				case "seen":
					err = SeenCmd(cmdArgs, out)
				case "inbox":
					err = InboxCmd(cmdArgs, out)
				case "post":
					err = PostCmd(cmdArgs, out)
				case "claim":
					err = ClaimCmd(cmdArgs, out)
				case "complete":
					err = CompleteCmd(cmdArgs, out)
				case "heartbeat":
					err = HeartbeatCmd(cmdArgs, out)
				case "abandon":
					err = AbandonCmd(cmdArgs, out)
				case "sweep":
					err = SweepCmd(cmdArgs, out)
				case "session-append", "session_append":
					err = SessionAppendCmd(cmdArgs, out)
				case "session-read", "session_read":
					err = SessionReadCmd(cmdArgs, out)
				default:
					err = fmt.Errorf("unknown slash command %q", cmdName)
				}

				if err != nil {
					if IsJSONOutput() {
						_ = writeJSONError(out, err)
					} else {
						fmt.Fprintf(out, "Error: %v\n", err)
					}
				}
			} else {
				// Publish chat message to topic
				reqPayload := map[string]any{
					"work_dir": info.Root,
					"actor":    actor,
					"topic":    topic,
					"body":     line,
				}
				var respMsg store.TopicMessage
				err := sendDaemonRequest(context.Background(), "topic_publish", reqPayload, &respMsg)
				if err != nil {
					if IsJSONOutput() {
						_ = writeJSONError(out, err)
					} else {
						fmt.Fprintf(out, "Error publishing message: %v\n", err)
					}
				}
			}
		}
	}()

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg store.TopicMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Print raw line if not parsed
			fmt.Fprintln(out, line)
			continue
		}

		if IsJSONOutput() {
			_ = writeJSONOutput(out, msg)
		} else {
			fmt.Fprintf(out, "[%d] %s: %s\n", msg.ID, msg.From, msg.Body)
		}
	}
}

func topicCursor(args []string, out io.Writer) error {
	var topic string
	var agent string
	var updateVal string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--topic=") {
			topic = strings.TrimPrefix(arg, "--topic=")
		} else if arg == "--topic" {
			i++
			if i < len(args) {
				topic = args[i]
			}
		} else if strings.HasPrefix(arg, "--agent=") {
			agent = strings.TrimPrefix(arg, "--agent=")
		} else if arg == "--agent" {
			i++
			if i < len(args) {
				agent = args[i]
			}
		} else if strings.HasPrefix(arg, "--update=") {
			updateVal = strings.TrimPrefix(arg, "--update=")
		} else if arg == "--update" {
			i++
			if i < len(args) {
				updateVal = args[i]
			}
		} else {
			return fmt.Errorf("unknown cursor argument %q", arg)
		}
	}

	if topic == "" {
		return errors.New("missing required --topic <name>")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	if agent == "" {
		agent = os.Getenv("COLLAB_ACTOR")
		if agent == "" {
			agent = info.Actor
		}
		if agent == "" {
			agent = "operator"
		}
	}

	if updateVal != "" {
		lastReadID, err := strconv.ParseInt(updateVal, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid message ID update value %q: %w", updateVal, err)
		}
		reqPayload := map[string]any{
			"work_dir":     info.Root,
			"actor":        agent,
			"topic":        topic,
			"last_read_id": lastReadID,
		}
		var result map[string]bool
		err = sendDaemonRequest(context.Background(), "topic_cursor_update", reqPayload, &result)
		if err != nil {
			return err
		}
		if IsJSONOutput() {
			return writeJSONOutput(out, result)
		}
		fmt.Fprintf(out, "Updated cursor for agent %q on topic %q to message ID %d\n", agent, topic, lastReadID)
		return nil
	}

	reqPayload := map[string]any{
		"work_dir": info.Root,
		"actor":    agent,
		"topic":    topic,
	}
	var resp struct {
		LastReadID int64 `json:"last_read_id"`
	}
	err = sendDaemonRequest(context.Background(), "topic_cursor_read", reqPayload, &resp)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, resp)
	}

	fmt.Fprintf(out, "Cursor offset: %d\n", resp.LastReadID)
	return nil
}
