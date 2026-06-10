package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/rlupi/botfam/internal/fam"
	"github.com/rlupi/botfam/internal/store"
)

type server struct {
	envActor string
	lockMode bool

	mu    sync.Mutex
	actor string
	locks map[string]*store.ActorLock
}

func Serve(in io.Reader, out io.Writer, errout io.Writer) error {
	s := &server{
		envActor: os.Getenv("COLLAB_ACTOR"),
		lockMode: lockActorEnabled(),
		locks:    make(map[string]*store.ActorLock),
	}
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, lock := range s.locks {
			_ = lock.Close()
		}
	}()

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
	st := store.New(info.Root)
	if err := st.Init(); err != nil {
		return nil, err
	}

	actor, err := s.resolveActor(argString(args, "actor"), info.Actor)
	if err != nil {
		return nil, err
	}
	var result any
	switch name {
	case "send":
		to := argString(args, "to")
		typ := argString(args, "type")
		msg, err := st.Send(actor, to, typ, argObject(args, "payload"), argString(args, "in_reply_to"), argFloatPtr(args, "expires_at"))
		result = msg
		if err != nil {
			return nil, err
		}
	case "recv":
		if err := s.ensureActorLock(actor, st); err != nil {
			return nil, err
		}
		timeout := time.Duration(argFloatDefault(args, "timeout_s", 120) * float64(time.Second))
		msg, err := st.Recv(ctx, actor, argString(args, "match_type"), timeout)
		if err != nil {
			return nil, err
		}
		result = msg
	case "try_recv":
		if err := s.ensureActorLock(actor, st); err != nil {
			return nil, err
		}
		msg, err := st.TryRecv(actor, argString(args, "match_type"))
		if err != nil {
			return nil, err
		}
		result = msg
	case "peek":
		msg, err := st.Peek(actor, argString(args, "match_type"))
		if err != nil {
			return nil, err
		}
		result = msg
	case "ack":
		if err := s.ensureActorLock(actor, st); err != nil {
			return nil, err
		}
		msg, err := st.Ack(actor, argString(args, "id"), args["outcome"])
		if err != nil {
			return nil, err
		}
		result = msg
	case "seen":
		seen, err := st.Seen(actor, argString(args, "id"))
		if err != nil {
			return nil, err
		}
		result = map[string]any{"seen": seen}
	case "inbox":
		snap, err := st.Inbox(actor)
		if err != nil {
			return nil, err
		}
		result = snap
	case "post":
		task, err := st.Post(actor, argStringDefault(args, "type", "task"), argObject(args, "payload"))
		if err != nil {
			return nil, err
		}
		result = task
	case "claim":
		task, err := st.Claim(actor, time.Duration(argFloatDefault(args, "lease_ttl", 120)*float64(time.Second)))
		if err != nil {
			return nil, err
		}
		result = task
	case "complete":
		task, err := st.Complete(actor, argString(args, "task_id"), args["result"])
		if err != nil {
			return nil, err
		}
		result = task
	case "heartbeat":
		task, err := st.Heartbeat(actor, argString(args, "task_id"), time.Duration(argFloatDefault(args, "lease_ttl", 120)*float64(time.Second)))
		if err != nil {
			return nil, err
		}
		result = task
	case "abandon":
		task, err := st.Abandon(actor, argString(args, "task_id"), argString(args, "reason"))
		if err != nil {
			return nil, err
		}
		result = task
	case "sweep":
		tasks, err := st.Sweep()
		if err != nil {
			return nil, err
		}
		result = map[string]any{"swept": tasks}
	case "session_append":
		if err := s.ensureActorLock(actor, st); err != nil {
			return nil, err
		}
		sessionName := argString(args, "session")
		body := argString(args, "body")
		var handoff *store.SessionHandoff
		if hObj, ok := args["handoff"].(map[string]any); ok && len(hObj) > 0 {
			handoff = &store.SessionHandoff{
				Task:        argString(hObj, "task"),
				Context:     argString(hObj, "context"),
				Deliverable: argString(hObj, "deliverable"),
			}
		}
		entry, err := st.SessionAppend(sessionName, actor, body, handoff)
		if err != nil {
			return nil, err
		}
		result = entry
	case "session_read":
		sessionName := argString(args, "session")
		filterActor := argString(args, "from")
		sinceTS := argFloatDefault(args, "since_ts", 0)
		limit := int(argFloatDefault(args, "limit", 0))
		entries, err := st.SessionRead(sessionName, filterActor, sinceTS, limit)
		if err != nil {
			return nil, err
		}
		result = entries
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
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
		return "", errors.New("identity required: pass actor, set COLLAB_ACTOR, or run from a named worktree")
	}
	if err := store.ValidateName("actor", candidate); err != nil {
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

func (s *server) ensureActorLock(actor string, st *store.Store) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[st.Root] != nil {
		return nil
	}
	lock, err := st.LockActor(actor)
	if err != nil {
		return err
	}
	s.locks[st.Root] = lock
	return st.RollbackProcessing(actor)
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
