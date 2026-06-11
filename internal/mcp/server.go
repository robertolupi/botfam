package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/robertolupi/botfam/internal/fam"
)

// errIdentityRequired signals that no actor identity could be resolved from
// any source (call arg, bound session, env, worktree directory).
var errIdentityRequired = errors.New("identity required: pass actor, set COLLAB_ACTOR, or run from a named worktree")

// identityOptionalTools are tools whose handlers never use the calling actor:
// session_read filters by the explicit "from" argument only, and sweep calls
// store.Sweep(), which takes no actor. For these, a missing identity is
// tolerated; identity conflicts are still rejected and a resolved identity
// still binds the session as usual.
var identityOptionalTools = map[string]bool{
	"session_read": true,
	"sweep":        true,
}

type server struct {
	envActor string
	lockMode bool

	mu    sync.Mutex
	actor string
}

func Serve(in io.Reader, out io.Writer, errout io.Writer) error {
	s := &server{
		envActor: os.Getenv("COLLAB_ACTOR"),
		lockMode: lockActorEnabled(),
	}
	mcpSrv := mcpserver.NewMCPServer("botfam", "0.1.0", mcpserver.WithToolCapabilities(false))
	s.registerTools(mcpSrv)
	return serveStdio(context.Background(), mcpSrv, in, out)
}

func (s *server) registerTools(mcpSrv *mcpserver.MCPServer) {
	add := func(tool mcplib.Tool) {
		mcpSrv.AddTool(tool, func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return s.callTool(ctx, req.Params.Name, req.GetArguments())
		})
	}

	add(mcplib.NewTool("send",
		mcplib.WithDescription("Send a message to another actor."),
		mcplib.WithString("to", mcplib.Required()),
		mcplib.WithString("type", mcplib.Required()),
		mcplib.WithObject("payload"),
		mcplib.WithString("in_reply_to"),
		mcplib.WithNumber("expires_at"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("recv",
		mcplib.WithDescription("Block until a message is reserved, or timeout."),
		mcplib.WithString("match_type"),
		mcplib.WithNumber("timeout_s"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("try_recv",
		mcplib.WithDescription("Reserve the oldest matching message if present."),
		mcplib.WithString("match_type"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("peek",
		mcplib.WithDescription("Inspect the oldest matching message without reserving it."),
		mcplib.WithString("match_type"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("ack",
		mcplib.WithDescription("Ack a reserved message."),
		mcplib.WithString("id", mcplib.Required()),
		mcplib.WithObject("outcome"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("seen",
		mcplib.WithDescription("Check whether a message id has been acked."),
		mcplib.WithString("id", mcplib.Required()),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("inbox",
		mcplib.WithDescription("Show mailbox and task counts."),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("post",
		mcplib.WithDescription("Post a task."),
		mcplib.WithString("type"),
		mcplib.WithObject("payload"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("claim",
		mcplib.WithDescription("Claim one open task."),
		mcplib.WithNumber("lease_ttl"),
		mcplib.WithString("task_id"),
		mcplib.WithString("type"),
		mcplib.WithString("suggested_owner"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("complete",
		mcplib.WithDescription("Complete an owned task."),
		mcplib.WithString("task_id", mcplib.Required()),
		mcplib.WithObject("result"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("heartbeat",
		mcplib.WithDescription("Extend an owned task lease."),
		mcplib.WithString("task_id", mcplib.Required()),
		mcplib.WithNumber("lease_ttl"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("abandon",
		mcplib.WithDescription("Release an owned task back to open."),
		mcplib.WithString("task_id", mcplib.Required()),
		mcplib.WithString("reason"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("sweep",
		mcplib.WithDescription("Return expired claimed tasks to open."),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("session_append",
		mcplib.WithDescription("Append an entry to a session log."),
		mcplib.WithString("session", mcplib.Required()),
		mcplib.WithString("body", mcplib.Required()),
		mcplib.WithObject("handoff"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("session_read",
		mcplib.WithDescription("Read entries from a session log."),
		mcplib.WithString("session", mcplib.Required()),
		mcplib.WithString("from"),
		mcplib.WithNumber("since_ts"),
		mcplib.WithNumber("limit"),
		mcplib.WithString("work_dir"),
	))
}

func (s *server) callTool(ctx context.Context, name string, args map[string]any) (*mcplib.CallToolResult, error) {
	workDir := argString(args, "work_dir")
	if workDir == "" {
		workDir = "."
	}
	info, err := (fam.Resolver{WorkDir: workDir, Env: os.Environ()}).Resolve()
	if err != nil {
		return nil, err
	}
	if err := fam.EnsureMembership(info.Root, info.Explicit, workDir); err != nil {
		return nil, err
	}

	actor, err := s.resolveActor(argString(args, "actor"), info.Actor)
	if err != nil {
		if !identityOptionalTools[name] || !errors.Is(err, errIdentityRequired) {
			return nil, err
		}
		actor = ""
	}

	// Ensure UDS daemon is running (auto-start)
	if err := ensureDaemon(); err != nil {
		return nil, err
	}

	// Route payload to UDS daemon
	payload := make(map[string]any)
	for k, v := range args {
		payload[k] = v
	}
	payload["actor"] = actor
	payload["work_dir"] = info.Root // Daemon expectsResolved info.Root as work_dir

	udsPath, err := getSocketPath()
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(dialCtx, "unix", udsPath)
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := "http://localhost/" + name
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error calling UDS daemon endpoint %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return nil, errors.New(errResp.Error)
		}
		return nil, fmt.Errorf("daemon endpoint %q returned status %s", name, resp.Status)
	}

	var result any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode daemon response: %w", err)
	}

	return toolResult(result)
}

func (s *server) resolveActor(callActor string, dirActor string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dirActor != "" {
		if callActor != "" && callActor != dirActor {
			return "", fmt.Errorf("actor %q conflicts with resolved directory actor %q", callActor, dirActor)
		}
		if s.envActor != "" && s.envActor != dirActor {
			return "", fmt.Errorf("COLLAB_ACTOR %q conflicts with resolved directory actor %q", s.envActor, dirActor)
		}
		if s.actor != "" && s.actor != dirActor {
			return "", fmt.Errorf("bound session actor %q conflicts with resolved directory actor %q", s.actor, dirActor)
		}
	}
	if s.lockMode {
		if s.envActor == "" {
			return "", errors.New("BOTFAM_LOCK_ACTOR is set but COLLAB_ACTOR is empty")
		}
		if callActor != "" && callActor != s.envActor {
			return "", fmt.Errorf("actor %q conflicts with locked COLLAB_ACTOR %q", callActor, s.envActor)
		}
		if s.actor == "" {
			s.actor = s.envActor
		}
		return s.actor, nil
	}
	candidate := callActor
	if candidate == "" {
		candidate = s.actor
	}
	if candidate == "" {
		candidate = s.envActor
	}
	if candidate == "" {
		candidate = dirActor
	}
	if candidate == "" {
		return "", errIdentityRequired
	}
	if err := validateActorName(candidate); err != nil {
		return "", err
	}
	if s.actor == "" {
		s.actor = candidate
		return candidate, nil
	}
	if callActor != "" && callActor != s.actor {
		return "", fmt.Errorf("actor %q conflicts with bound session actor %q", callActor, s.actor)
	}
	return s.actor, nil
}

func validateActorName(name string) error {
	if name == "" {
		return errors.New("actor name cannot be empty")
	}
	for _, r := range name {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("invalid actor %q: must match [A-Za-z0-9_-]+", name)
		}
	}
	return nil
}

func getSocketPath() (string, error) {
	if path := os.Getenv("BOTFAM_SOCKET"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".botfam", "daemon.sock")
	if len(path) > 104 {
		h := sha256.Sum256([]byte(home))
		path = filepath.Join("/tmp", fmt.Sprintf("bf-%s.sock", hex.EncodeToString(h[:])))
	}
	return path, nil
}

func ensureDaemon() error {
	udsPath, err := getSocketPath()
	if err != nil {
		return err
	}

	// Dial socket to see if running
	conn, err := net.Dial("unix", udsPath)
	if err == nil {
		conn.Close()
		return nil
	}

	// If BOTFAM_SOCKET is set explicitly (e.g. in tests), do not auto-spawn a background process.
	// We expect the test runner to manage the test server lifecycle.
	if os.Getenv("BOTFAM_SOCKET") != "" {
		return fmt.Errorf("UDS daemon not running at %s", udsPath)
	}

	_ = os.Remove(udsPath)

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
	}
	if os.Getenv("BOTFAM_TESTING") != "1" {
		if homeBin := filepath.Join(home, "bin", "botfam"); fileExists(homeBin) {
			execPath = homeBin
		}
	}

	cmd := exec.Command(execPath, "server", "--port=0")
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	// Redirect stdout/stderr to a log file for debugging
	logFile, _ := os.OpenFile(filepath.Join(filepath.Dir(udsPath), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		defer logFile.Close()
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.Dial("unix", udsPath)
		if err == nil {
			conn.Close()
			return nil
		}
	}
	logBytes, _ := os.ReadFile(filepath.Join(filepath.Dir(udsPath), "daemon.log"))
	logStr := ""
	if len(logBytes) > 0 {
		logStr = "\nDaemon Log:\n" + string(logBytes)
	}
	return fmt.Errorf("daemon did not start UDS listener within 5s%s", logStr)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func toolResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcplib.NewToolResultText(string(b)), nil
}

// serveStdio implements the MCP stdio transport: messages are newline-delimited
// JSON, one per line, with no framing headers. The reader (readFrame) also
// tolerates legacy Content-Length-framed input, but responses are always written
// as a single line of JSON terminated by '\n'.
func serveStdio(ctx context.Context, mcpSrv *mcpserver.MCPServer, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	var writeMu sync.Mutex
	for {
		body, err := readFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		resp := mcpSrv.HandleMessage(ctx, body)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		writeMu.Lock()
		_, err = out.Write(b)
		writeMu.Unlock()
		if err != nil {
			return err
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return []byte(trimmed), nil
		}

		contentLen := 0
		for {
			k, v, ok := strings.Cut(trimmed, ":")
			if ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return nil, err
				}
				contentLen = n
			}
			line, err = r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			trimmed = strings.TrimSpace(line)
			if trimmed == "" {
				break
			}
		}
		if contentLen <= 0 {
			return nil, errors.New("missing Content-Length")
		}
		body := make([]byte, contentLen)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func lockActorEnabled() bool {
	if os.Getenv("BOTFAM_LOCK_ACTOR") == "1" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "botfam", "config")
	if os.Getenv("XDG_CONFIG_HOME") == "" {
		path = filepath.Join(home, ".config", "botfam", "config")
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "lock_actor = true")
}

func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func argStringDefault(args map[string]any, key, def string) string {
	if v := argString(args, key); v != "" {
		return v
	}
	return def
}

func argObject(args map[string]any, key string) map[string]any {
	if v, ok := args[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func argFloatDefault(args map[string]any, key string, def float64) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return def
	}
}

func argFloatPtr(args map[string]any, key string) *float64 {
	if _, ok := args[key]; !ok {
		return nil
	}
	v := argFloatDefault(args, key, 0)
	return &v
}
