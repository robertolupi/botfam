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

	"github.com/rlupi/botfam/internal/fam"
	"github.com/rlupi/botfam/internal/store"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct {
	in       *bufio.Reader
	out      io.Writer
	errout   io.Writer
	writeMu  sync.Mutex
	store    *store.Store
	envActor string
	lockMode bool

	actorMu sync.Mutex
	actor   string
	lock    *store.ActorLock
}

func Serve(in io.Reader, out io.Writer, errout io.Writer) error {
	info, err := (fam.Resolver{WorkDir: ".", Env: os.Environ()}).Resolve()
	if err != nil {
		return err
	}
	if err := fam.EnsureMembership(info.Root, info.Explicit, "."); err != nil {
		return err
	}
	st := store.New(info.Root)
	if err := st.Init(); err != nil {
		return err
	}
	s := &server{
		in:       bufio.NewReader(in),
		out:      out,
		errout:   errout,
		store:    st,
		envActor: os.Getenv("COLLAB_ACTOR"),
		lockMode: lockActorEnabled(),
	}
	defer func() {
		if s.lock != nil {
			_ = s.lock.Close()
		}
	}()
	for {
		req, err := readFrame(s.in)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if req.ID == nil {
			continue
		}
		result, callErr := s.handle(context.Background(), req)
		if callErr != nil {
			_ = s.write(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32000, Message: callErr.Error()}})
			continue
		}
		if err := s.write(response{JSONRPC: "2.0", ID: req.ID, Result: result}); err != nil {
			return err
		}
	}
}

func (s *server) handle(ctx context.Context, req request) (any, error) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2025-06-18",
			"serverInfo":      map[string]any{"name": "botfam", "version": "0.1.0"},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		return s.callTool(ctx, p.Name, p.Arguments)
	default:
		return nil, fmt.Errorf("unsupported method %s", req.Method)
	}
}

func (s *server) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	actor, err := s.resolveActor(argString(args, "actor"))
	if err != nil {
		return nil, err
	}
	var result any
	switch name {
	case "send":
		to := argString(args, "to")
		typ := argString(args, "type")
		msg, err := s.store.Send(actor, to, typ, argObject(args, "payload"), argString(args, "in_reply_to"), argFloatPtr(args, "expires_at"))
		result = msg
		if err != nil {
			return nil, err
		}
	case "recv":
		if err := s.ensureActorLock(actor); err != nil {
			return nil, err
		}
		timeout := time.Duration(argFloatDefault(args, "timeout_s", 120) * float64(time.Second))
		msg, err := s.store.Recv(ctx, actor, argString(args, "match_type"), timeout)
		if err != nil {
			return nil, err
		}
		result = msg
	case "try_recv":
		if err := s.ensureActorLock(actor); err != nil {
			return nil, err
		}
		msg, err := s.store.TryRecv(actor, argString(args, "match_type"))
		if err != nil {
			return nil, err
		}
		result = msg
	case "peek":
		msg, err := s.store.Peek(actor, argString(args, "match_type"))
		if err != nil {
			return nil, err
		}
		result = msg
	case "ack":
		if err := s.ensureActorLock(actor); err != nil {
			return nil, err
		}
		msg, err := s.store.Ack(actor, argString(args, "id"), args["outcome"])
		if err != nil {
			return nil, err
		}
		result = msg
	case "seen":
		seen, err := s.store.Seen(actor, argString(args, "id"))
		if err != nil {
			return nil, err
		}
		result = map[string]any{"seen": seen}
	case "inbox":
		snap, err := s.store.Inbox(actor)
		if err != nil {
			return nil, err
		}
		result = snap
	case "post":
		task, err := s.store.Post(actor, argStringDefault(args, "type", "task"), argObject(args, "payload"))
		if err != nil {
			return nil, err
		}
		result = task
	case "claim":
		task, err := s.store.Claim(actor, time.Duration(argFloatDefault(args, "lease_ttl", 120)*float64(time.Second)))
		if err != nil {
			return nil, err
		}
		result = task
	case "complete":
		task, err := s.store.Complete(actor, argString(args, "task_id"), args["result"])
		if err != nil {
			return nil, err
		}
		result = task
	case "heartbeat":
		task, err := s.store.Heartbeat(actor, argString(args, "task_id"), time.Duration(argFloatDefault(args, "lease_ttl", 120)*float64(time.Second)))
		if err != nil {
			return nil, err
		}
		result = task
	case "abandon":
		task, err := s.store.Abandon(actor, argString(args, "task_id"), argString(args, "reason"))
		if err != nil {
			return nil, err
		}
		result = task
	case "sweep":
		tasks, err := s.store.Sweep()
		if err != nil {
			return nil, err
		}
		result = map[string]any{"swept": tasks}
	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return toolResult(result)
}

func (s *server) resolveActor(callActor string) (string, error) {
	s.actorMu.Lock()
	defer s.actorMu.Unlock()
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
		return "", errors.New("identity required: pass actor or set COLLAB_ACTOR")
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

func (s *server) ensureActorLock(actor string) error {
	s.actorMu.Lock()
	defer s.actorMu.Unlock()
	if s.lock != nil {
		return nil
	}
	lock, err := s.store.LockActor(actor)
	if err != nil {
		return err
	}
	s.lock = lock
	return s.store.RollbackProcessing(actor)
}

func toolResult(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
	}, nil
}

func readFrame(r *bufio.Reader) (request, error) {
	var contentLen int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return request{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return request{}, err
			}
			contentLen = n
		}
	}
	if contentLen <= 0 {
		return request{}, errors.New("missing Content-Length")
	}
	body := make([]byte, contentLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return request{}, err
	}
	var req request
	return req, json.Unmarshal(body, &req)
}

func (s *server) write(resp response) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
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
