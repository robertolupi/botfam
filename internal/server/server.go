package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rlupi/botfam/internal/fam"
	"github.com/rlupi/botfam/internal/store"
)

type pendingRecv struct {
	actor     string
	matchType string
	ch        chan *store.Message
}

type FamilyState struct {
	mu            sync.Mutex
	store         store.Store
	subscriptions []*pendingRecv
	locks         map[string]*store.ActorLock
	lockPIDs      map[string]int
	activeActors  map[string]time.Time // last seen timestamps
}

type voteConnection struct {
	proposalID string
	actor      string
	workDir    string
	verdict    string
	commitSHA  string
	cancel     chan struct{}
	conn       net.Conn
}

type connKeyType struct{}
var connKey = connKeyType{}

type Server struct {
	udsPath   string
	tcpPort   int
	operator  string
	mu        sync.Mutex
	families  map[string]*FamilyState
	votes     map[string]map[string]*voteConnection // proposalID -> actor -> voteConnection
	clients   map[string]chan string                // SSE clients for operator UI
	clientsMu sync.Mutex
}

func NewServer(udsPath string, tcpPort int) *Server {
	return &Server{
		udsPath:  udsPath,
		tcpPort:  tcpPort,
		families: make(map[string]*FamilyState),
		votes:    make(map[string]map[string]*voteConnection),
		clients:  make(map[string]chan string),
	}
}

func (s *Server) Start(ctx context.Context) error {
	// Setup UDS socket
	_ = os.Remove(s.udsPath)
	if err := os.MkdirAll(filepath.Dir(s.udsPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for socket: %w", err)
	}
	udsListener, err := net.Listen("unix", s.udsPath)
	if err != nil {
		return fmt.Errorf("uds listen error: %w", err)
	}
	defer udsListener.Close()
	defer os.Remove(s.udsPath)
	_ = os.Chmod(s.udsPath, 0777)

	// Setup TCP socket for Operator UI
	tcpAddr := fmt.Sprintf("localhost:%d", s.tcpPort)
	tcpListener, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return fmt.Errorf("tcp listen error: %w", err)
	}
	defer tcpListener.Close()

	// Create unified HTTP handler
	handler := s.setupHandler()

	udsServer := &http.Server{
		Handler: handler,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey, c)
		},
	}
	tcpServer := &http.Server{
		Handler: handler,
	}

	errCh := make(chan error, 2)
	go func() {
		// Serve HTTP over UDS
		errCh <- udsServer.Serve(udsListener)
	}()
	go func() {
		// Serve HTTP over TCP (localhost only)
		errCh <- tcpServer.Serve(tcpListener)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		_ = udsServer.Close()
		_ = tcpServer.Close()
		return nil
	}
}

func (s *Server) getFamily(workDir string) (*FamilyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	root, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}

	if _, ok := s.families[root]; !ok {
		st := store.New(root)
		if err := st.Init(); err != nil {
			return nil, err
		}
		s.families[root] = &FamilyState{
			store:        st,
			locks:        make(map[string]*store.ActorLock),
			lockPIDs:     make(map[string]int),
			activeActors: make(map[string]time.Time),
		}
	}
	return s.families[root], nil
}

func (s *Server) setupHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/recv", s.handleRecv)
	mux.HandleFunc("/try_recv", s.handleTryRecv)
	mux.HandleFunc("/peek", s.handlePeek)
	mux.HandleFunc("/ack", s.handleAck)
	mux.HandleFunc("/seen", s.handleSeen)
	mux.HandleFunc("/inbox", s.handleInbox)
	mux.HandleFunc("/post", s.handlePost)
	mux.HandleFunc("/claim", s.handleClaim)
	mux.HandleFunc("/complete", s.handleComplete)
	mux.HandleFunc("/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/abandon", s.handleAbandon)
	mux.HandleFunc("/sweep", s.handleSweep)
	mux.HandleFunc("/session_append", s.handleSessionAppend)
	mux.HandleFunc("/session_read", s.handleSessionRead)

	// Voting and Tally endpoints
	mux.HandleFunc("/vote", s.handleVote)
	mux.HandleFunc("/tally", s.handleTally)

	// Operator UI endpoints
	mux.HandleFunc("/ui", s.handleUI)
	mux.HandleFunc("/ui/events", s.handleUIEvents)
	mux.HandleFunc("/ui/data", s.handleUIData)
	mux.HandleFunc("/ui/vote", s.handleUIVote)
	mux.HandleFunc("/ui/comment", s.handleUIComment)

	return mux
}

// Helper to write JSON error
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Helper to write JSON success
func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir   string         `json:"work_dir"`
		Actor     string         `json:"actor"`
		To        string         `json:"to"`
		Type      string         `json:"type"`
		Payload   map[string]any `json:"payload"`
		InReplyTo string         `json:"in_reply_to"`
		ExpiresAt *float64       `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	msg, err := fs.store.Send(req.Actor, req.To, req.Type, req.Payload, req.InReplyTo, req.ExpiresAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Update presence last_seen
	fs.activeActors[req.Actor] = time.Now()

	// Notify any matching pending receives
	for i, sub := range fs.subscriptions {
		if sub.actor == req.To && (sub.matchType == "" || sub.matchType == req.Type) {
			select {
			case sub.ch <- &msg:
				// Successfully sent, remove subscription
				fs.subscriptions = append(fs.subscriptions[:i], fs.subscriptions[i+1:]...)
			default:
				// Channel buffer full or closed, ignore
			}
			break
		}
	}

	s.broadcastEvent(fmt.Sprintf("send:%s:%s", req.Actor, req.To))
	writeJSON(w, msg)
}

func (s *Server) handleRecv(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir   string  `json:"work_dir"`
		Actor     string  `json:"actor"`
		MatchType string  `json:"match_type"`
		TimeoutS  float64 `json:"timeout_s"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	// Check store for any immediate message
	msg, err := fs.store.TryRecv(req.Actor, req.MatchType)
	if err != nil {
		fs.mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msg != nil {
		fs.activeActors[req.Actor] = time.Now()
		fs.mu.Unlock()
		writeJSON(w, msg)
		return
	}

	// Lock actor for stdio safety equivalent
	pid := getRequestPID(r)
	if err := s.ensureActorLock(fs, req.Actor, pid); err != nil {
		fs.mu.Unlock()
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	// Wait on in-memory channel
	ch := make(chan *store.Message, 1)
	sub := &pendingRecv{
		actor:     req.Actor,
		matchType: req.MatchType,
		ch:        ch,
	}
	fs.subscriptions = append(fs.subscriptions, sub)
	fs.activeActors[req.Actor] = time.Now()
	fs.mu.Unlock()

	timeout := 120 * time.Second
	if req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS * float64(time.Second))
	}

	select {
	case <-ch:
		fs.mu.Lock()
		msg, err := fs.store.TryRecv(req.Actor, req.MatchType)
		fs.mu.Unlock()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, msg)
	case <-time.After(timeout):
		// Remove subscription on timeout
		fs.mu.Lock()
		for i, p := range fs.subscriptions {
			if p == sub {
				fs.subscriptions = append(fs.subscriptions[:i], fs.subscriptions[i+1:]...)
				break
			}
		}
		fs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	case <-r.Context().Done():
		// Clean up subscription if client disconnected
		fs.mu.Lock()
		for i, p := range fs.subscriptions {
			if p == sub {
				fs.subscriptions = append(fs.subscriptions[:i], fs.subscriptions[i+1:]...)
				break
			}
		}
		fs.mu.Unlock()
	}
}

func (s *Server) handleTryRecv(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir   string `json:"work_dir"`
		Actor     string `json:"actor"`
		MatchType string `json:"match_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	pid := getRequestPID(r)
	if err := s.ensureActorLock(fs, req.Actor, pid); err != nil {
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	msg, err := fs.store.TryRecv(req.Actor, req.MatchType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	writeJSON(w, msg)
}

func (s *Server) handlePeek(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir   string `json:"work_dir"`
		Actor     string `json:"actor"`
		MatchType string `json:"match_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	msg, err := fs.store.Peek(req.Actor, req.MatchType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, msg)
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string         `json:"work_dir"`
		Actor   string         `json:"actor"`
		ID      string         `json:"id"`
		Outcome map[string]any `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	pid := getRequestPID(r)
	if err := s.ensureActorLock(fs, req.Actor, pid); err != nil {
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	msg, err := fs.store.Ack(req.Actor, req.ID, req.Outcome)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("ack:%s:%s", req.Actor, req.ID))
	writeJSON(w, msg)
}

func (s *Server) handleSeen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string `json:"work_dir"`
		Actor   string `json:"actor"`
		ID      string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	seen, err := fs.store.Seen(req.Actor, req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"seen": seen})
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string `json:"work_dir"`
		Actor   string `json:"actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	snap, err := fs.store.Inbox(req.Actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	writeJSON(w, snap)
}

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string         `json:"work_dir"`
		Actor   string         `json:"actor"`
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	task, err := fs.store.Post(req.Actor, req.Type, req.Payload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("task:post:%s", task.ID))
	writeJSON(w, task)
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir        string  `json:"work_dir"`
		Actor          string  `json:"actor"`
		LeaseTTL       float64 `json:"lease_ttl"`
		TaskID         string  `json:"task_id"`
		Type           string  `json:"type"`
		SuggestedOwner string  `json:"suggested_owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	ttl := 120 * time.Second
	if req.LeaseTTL != 0 {
		ttl = time.Duration(req.LeaseTTL * float64(time.Second))
	}

	task, err := fs.store.Claim(req.Actor, ttl, store.ClaimOptions{
		TaskID:         req.TaskID,
		Type:           req.Type,
		SuggestedOwner: req.SuggestedOwner,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("task:claim:%s", task.ID))
	writeJSON(w, task)
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string         `json:"work_dir"`
		Actor   string         `json:"actor"`
		TaskID  string         `json:"task_id"`
		Result  map[string]any `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	task, err := fs.store.Complete(req.Actor, req.TaskID, req.Result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("task:complete:%s", task.ID))
	writeJSON(w, task)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir  string  `json:"work_dir"`
		Actor    string  `json:"actor"`
		TaskID   string  `json:"task_id"`
		LeaseTTL float64 `json:"lease_ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	ttl := 120 * time.Second
	if req.LeaseTTL != 0 {
		ttl = time.Duration(req.LeaseTTL * float64(time.Second))
	}

	task, err := fs.store.Heartbeat(req.Actor, req.TaskID, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	writeJSON(w, task)
}

func (s *Server) handleAbandon(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string `json:"work_dir"`
		Actor   string `json:"actor"`
		TaskID  string `json:"task_id"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	task, err := fs.store.Abandon(req.Actor, req.TaskID, req.Reason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("task:abandon:%s", task.ID))
	writeJSON(w, task)
}

func (s *Server) handleSweep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string `json:"work_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	tasks, err := fs.store.Sweep()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"swept": tasks})
}

func (s *Server) handleSessionAppend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string                `json:"work_dir"`
		Actor   string                `json:"actor"`
		Session string                `json:"session"`
		Body    string                `json:"body"`
		Handoff *store.SessionHandoff `json:"handoff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	pid := getRequestPID(r)
	if err := s.ensureActorLock(fs, req.Actor, pid); err != nil {
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	entry, err := fs.store.SessionAppend(req.Session, req.Actor, req.Body, req.Handoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Release any active in-memory vote connections if this is a ccrep:executed event
	var entryBody struct {
		Type       string `json:"type"`
		ProposalID string `json:"proposal_id"`
	}
	if json.Unmarshal([]byte(req.Body), &entryBody) == nil {
		if entryBody.Type == "ccrep:executed" && entryBody.ProposalID != "" {
			s.mu.Lock()
			if pVotes, ok := s.votes[entryBody.ProposalID]; ok {
				for _, v := range pVotes {
					if v.cancel != nil {
						close(v.cancel)
					}
					if v.conn != nil {
						_ = v.conn.Close()
					}
				}
				delete(s.votes, entryBody.ProposalID)
			}
			s.mu.Unlock()
		}
	}
	fs.activeActors[req.Actor] = time.Now()
	s.broadcastEvent(fmt.Sprintf("session:append:%s", req.Session))
	writeJSON(w, entry)
}

func (s *Server) handleSessionRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string  `json:"work_dir"`
		Session string  `json:"session"`
		From    string  `json:"from"`
		SinceTS float64 `json:"since_ts"`
		Limit   int     `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	entries, err := fs.store.SessionRead(req.Session, req.From, req.SinceTS, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, entries)
}

func (s *Server) handleVote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir    string `json:"work_dir"`
		Actor      string `json:"actor"`
		ProposalID string `json:"proposal_id"`
		Verdict    string `json:"verdict"`
		CommitSHA  string `json:"commit_sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.validateRequestActor(r, req.Actor, req.WorkDir); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "webserver does not support hijacking")
		return
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("hijack failed: %v", err))
		return
	}

	// Write the HTTP response headers and JSON body to the hijacked connection.
	respBody, _ := json.Marshal(map[string]string{"status": "registered"})
	respStr := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n%s", len(respBody), string(respBody))
	_, _ = conn.Write([]byte(respStr))

	s.mu.Lock()
	if _, ok := s.votes[req.ProposalID]; !ok {
		s.votes[req.ProposalID] = make(map[string]*voteConnection)
	}

	cancelCh := make(chan struct{})
	vote := &voteConnection{
		proposalID: req.ProposalID,
		actor:      req.Actor,
		workDir:    req.WorkDir,
		verdict:    req.Verdict,
		commitSHA:  req.CommitSHA,
		cancel:     cancelCh,
		conn:       conn,
	}

	// Reconnect replaces previous connection under same actor name
	if prev, ok := s.votes[req.ProposalID][req.Actor]; ok {
		if prev.cancel != nil {
			close(prev.cancel)
		}
		if prev.conn != nil {
			type closeWriter interface {
				CloseWrite() error
			}
			if cw, ok := prev.conn.(closeWriter); ok {
				_ = cw.CloseWrite()
			}
			_ = prev.conn.Close()
		}
	}
	s.votes[req.ProposalID][req.Actor] = vote
	s.mu.Unlock()

	s.broadcastEvent(fmt.Sprintf("vote:%s:%s:%s", req.ProposalID, req.Actor, req.Verdict))

	disconnectCh := make(chan struct{})
	go func() {
		defer close(disconnectCh)
		buf := make([]byte, 1024)
		for {
			_, err := conn.Read(buf)
			if err != nil {
				break
			}
		}
	}()

	select {
	case <-cancelCh:
		// Replaced or resolved by server
		type closeWriter interface {
			CloseWrite() error
		}
		if cw, ok := conn.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
		_ = conn.Close()
	case <-disconnectCh:
		// Client dropped connection, withdraw vote
		s.mu.Lock()
		if curr, ok := s.votes[req.ProposalID][req.Actor]; ok && curr == vote {
			delete(s.votes[req.ProposalID], req.Actor)
		}
		s.mu.Unlock()
		s.broadcastEvent(fmt.Sprintf("vote:withdrawn:%s:%s", req.ProposalID, req.Actor))
	}
}

type uiVoteInfo struct {
	Actor      string    `json:"actor"`
	Verdict    string    `json:"verdict"`
	CommitSHA  string    `json:"commit_sha"`
	Timestamp  time.Time `json:"timestamp"`
	IsPresent  bool      `json:"is_present"`
	Provenance string    `json:"provenance"`
}

type uiTallyResult struct {
	ProposalID   string                `json:"proposal_id"`
	Status       string                `json:"status"`
	Votes        map[string]uiVoteInfo `json:"votes"`
	DecisionRule string                `json:"decision_rule"`
	LatestSHA    string                `json:"latest_sha"`
	Author       string                `json:"author"`
}

func (s *Server) isActorPresent(fs *FamilyState, actor string, proposalID string) bool {
	if actor == "operator" {
		return true
	}
	s.mu.Lock()
	// 1. Check active votes in-memory
	if pv, ok := s.votes[proposalID]; ok {
		if _, ok := pv[actor]; ok {
			s.mu.Unlock()
			return true
		}
	}
	s.mu.Unlock()

	// 2. Check active lock and if that lock's process is alive
	if fs.locks[actor] != nil {
		pid := fs.lockPIDs[actor]
		if pid > 0 && isProcessAlive(pid) {
			return true
		}
	}

	// 3. Check presence last_seen within 30 minutes
	if lastSeen, ok := fs.activeActors[actor]; ok {
		if time.Since(lastSeen) <= 30*time.Minute {
			return true
		}
	}

	return false
}

func (s *Server) computeTallyInternal(fs *FamilyState, proposalID string) (*uiTallyResult, error) {
	latestSHA, deadline, quorumRule, err := getProposalDetails(fs.store, proposalID)
	if err != nil {
		return nil, err
	}

	if latestSHA == "" {
		return &uiTallyResult{
			ProposalID:   proposalID,
			Status:       "PENDING",
			Votes:        map[string]uiVoteInfo{},
			DecisionRule: "majority",
		}, nil
	}

	// Read session meta.json to check constitution fields
	var sessionMeta struct {
		DecisionRule string   `json:"decision_rule"`
		Participants []string `json:"participants"`
	}
	metaPath := filepath.Join(fs.store.RootPath(), "sessions", proposalID, "meta.json")
	if b, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(b, &sessionMeta)
	}

	participants := sessionMeta.Participants
	if len(participants) == 0 {
		participants, _ = getRoster(fs.store.RootPath())
	}

	s.mu.Lock()
	proposalVotes := make(map[string]*voteConnection)
	if pv, ok := s.votes[proposalID]; ok {
		for k, v := range pv {
			proposalVotes[k] = v
		}
	}
	s.mu.Unlock()

	tallies := make(map[string]uiVoteInfo)
	for actor, v := range proposalVotes {
		provenance := "cwd-corroborated"
		if actor == "operator" {
			provenance = "operator-ui"
		}
		tallies[actor] = uiVoteInfo{
			Actor:      actor,
			Verdict:    v.verdict,
			CommitSHA:  v.commitSHA,
			Timestamp:  time.Now(),
			IsPresent:  true,
			Provenance: provenance,
		}
	}

	// Find the author of the proposal
	events, _, err := fam.CollectCcrepEvents(fs.store, proposalID)
	var author string
	if err == nil {
		for _, ev := range events {
			if ev.Type == "ccrep:proposal" {
				author = ev.Reviewer
				break
			}
		}
	}

	var presentCount int
	var approvals int
	var blocks int

	for _, p := range participants {
		if p == author {
			continue
		}
		if s.isActorPresent(fs, p, proposalID) {
			presentCount++
			if v, ok := tallies[p]; ok {
				vLower := strings.ToLower(v.Verdict)
				if vLower == "approve" {
					if v.CommitSHA == latestSHA {
						approvals++
					}
				} else if vLower == "request_changes" || vLower == "reject" {
					blocks++
				}
			}
		}
	}

	var operatorVeto bool
	if opVote, ok := tallies["operator"]; ok {
		vLower := strings.ToLower(opVote.Verdict)
		if vLower == "reject" || vLower == "request_changes" {
			operatorVeto = true
		}
	}

	status := "PENDING"
	if blocks > 0 || operatorVeto {
		status = "BLOCKED"
	} else {
		ruleName := sessionMeta.DecisionRule
		if ruleName == "" {
			ruleName = quorumRule
		}
		rule := strings.ToLower(ruleName)
		if rule == "all" || rule == "consensus" {
			if approvals == presentCount && presentCount > 0 {
				status = "MET"
			}
		} else if rule == "any" {
			if approvals >= 1 {
				status = "MET"
			}
		} else { // default is "majority"
			if approvals > presentCount/2 && presentCount > 0 {
				status = "MET"
			}
		}
	}

	if status == "PENDING" && deadline > 0 && float64(time.Now().Unix()) > deadline {
		status = "EXPIRED"
	}

	ruleName := sessionMeta.DecisionRule
	if ruleName == "" {
		ruleName = quorumRule
	}
	if ruleName == "" {
		ruleName = "majority"
	}

	return &uiTallyResult{
		ProposalID:   proposalID,
		Status:       status,
		Votes:        tallies,
		DecisionRule: ruleName,
		LatestSHA:    latestSHA,
		Author:       author,
	}, nil
}

func (s *Server) handleTally(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir    string `json:"work_dir"`
		ProposalID string `json:"proposal_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	res, err := s.computeTallyInternal(fs, req.ProposalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, res)
}

func getProposalDetails(st store.Store, proposalID string) (latestSHA string, deadline float64, quorum string, err error) {
	events, _, err := fam.CollectCcrepEvents(st, proposalID)
	if err != nil {
		return "", 0, "", err
	}
	quorum = "majority"
	for _, ev := range events {
		if ev.Type == "ccrep:proposal" {
			latestSHA = ev.CommitSHA
		} else if ev.Type == "ccrep:revision" {
			latestSHA = ev.CommitSHA
		}
	}

	// Search maildirs to find the ccrep:proposal payload for quorum/deadline
	files, err := os.ReadDir(st.RootPath())
	if err == nil {
		for _, f := range files {
			if !f.IsDir() || f.Name() == "tmp" || f.Name() == "tasks" || f.Name() == "sessions" || strings.HasPrefix(f.Name(), ".") {
				continue
			}
			actor := f.Name()
			for _, sub := range []string{"new", "processing", "cur"} {
				subDir := filepath.Join(st.RootPath(), actor, sub)
				msgs, err := os.ReadDir(subDir)
				if err != nil {
					continue
				}
				for _, m := range msgs {
					if !strings.HasSuffix(m.Name(), ".json") {
						continue
					}
					b, err := os.ReadFile(filepath.Join(subDir, m.Name()))
					if err != nil {
						continue
					}
					var msg struct {
						Type    string `json:"type"`
						Payload struct {
							ProposalID string   `json:"proposal_id"`
							Quorum     string   `json:"quorum"`
							Deadline   *float64 `json:"deadline"`
						} `json:"payload"`
					}
					if json.Unmarshal(b, &msg) == nil {
						if msg.Type == "ccrep:proposal" && msg.Payload.ProposalID == proposalID {
							if msg.Payload.Quorum != "" {
								quorum = msg.Payload.Quorum
							}
							if msg.Payload.Deadline != nil {
								deadline = *msg.Payload.Deadline
							}
							return latestSHA, deadline, quorum, nil
						}
					}
				}
			}
		}
	}
	return latestSHA, deadline, quorum, nil
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	html := `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>botfam Operator Console</title>
	<link rel="preconnect" href="https://fonts.googleapis.com">
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
	<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&family=Space+Grotesk:wght@400;700&display=swap" rel="stylesheet">
	<style>
		:root {
			--bg-grad: radial-gradient(circle at top right, #1d1b26, #0d0c10 70%);
			--panel-bg: rgba(22, 20, 30, 0.6);
			--border-color: rgba(255, 255, 255, 0.08);
			--glow-color: rgba(255, 87, 34, 0.15);
			--text-main: #f3f1f6;
			--text-muted: #9c99a6;
			--accent: #ff5722;
			--accent-glow: 0 0 20px rgba(255, 87, 34, 0.4);
			--online: #4caf50;
			--away: #ff9800;
			--offline: #9e9e9e;
		}
		* { box-sizing: border-box; }
		body {
			margin: 0;
			font-family: 'Outfit', sans-serif;
			background: var(--bg-grad);
			color: var(--text-main);
			min-height: 100vh;
			display: flex;
			flex-direction: column;
		}
		header {
			padding: 24px 40px;
			border-bottom: 1px solid var(--border-color);
			display: flex;
			align-items: center;
			justify-content: space-between;
			background: rgba(13, 12, 16, 0.5);
			backdrop-filter: blur(12px);
		}
		h1 {
			margin: 0;
			font-size: 28px;
			font-weight: 800;
			background: linear-gradient(135deg, #fff, var(--accent));
			-webkit-background-clip: text;
			-webkit-text-fill-color: transparent;
			font-family: 'Space Grotesk', sans-serif;
		}
		.container {
			flex: 1;
			display: grid;
			grid-template-columns: 320px 1fr;
			gap: 32px;
			padding: 40px;
			max-width: 1600px;
			margin: 0 auto;
			width: 100%;
		}
		.sidebar {
			display: flex;
			flex-direction: column;
			gap: 32px;
		}
		.card {
			background: var(--panel-bg);
			border: 1px solid var(--border-color);
			border-radius: 20px;
			padding: 24px;
			backdrop-filter: blur(16px);
			box-shadow: 0 8px 32px 0 rgba(0, 0, 0, 0.37);
			position: relative;
			overflow: hidden;
			transition: border-color 0.3s ease, box-shadow 0.3s ease;
		}
		.card::before {
			content: '';
			position: absolute;
			top: 0; left: 0; width: 100%; height: 100%;
			background: radial-gradient(circle at top left, rgba(255,87,34,0.03), transparent 60%);
			pointer-events: none;
		}
		.card:hover {
			border-color: rgba(255, 87, 34, 0.2);
			box-shadow: 0 8px 32px var(--glow-color);
		}
		h2 {
			margin: 0 0 20px 0;
			font-size: 20px;
			font-weight: 700;
			font-family: 'Space Grotesk', sans-serif;
			display: flex;
			align-items: center;
			justify-content: space-between;
		}
		.roster-list {
			display: flex;
			flex-direction: column;
			gap: 16px;
		}
		.roster-item {
			display: flex;
			align-items: center;
			justify-content: space-between;
			padding: 12px 16px;
			background: rgba(255, 255, 255, 0.02);
			border-radius: 12px;
			border: 1px solid rgba(255, 255, 255, 0.04);
			transition: background 0.2s ease;
		}
		.roster-item:hover {
			background: rgba(255, 255, 255, 0.05);
		}
		.actor-name {
			font-weight: 600;
			font-size: 16px;
		}
		.presence-badge {
			display: flex;
			align-items: center;
			gap: 8px;
			font-size: 14px;
			text-transform: capitalize;
			font-weight: 600;
		}
		.dot {
			width: 10px;
			height: 10px;
			border-radius: 50%;
			display: inline-block;
		}
		.dot.online { background: var(--online); box-shadow: 0 0 10px var(--online); }
		.dot.away { background: var(--away); box-shadow: 0 0 10px var(--away); }
		.dot.offline { background: var(--offline); }
		
		.main-content {
			display: flex;
			flex-direction: column;
			gap: 32px;
		}
		.session-card {
			margin-bottom: 24px;
		}
		.session-header {
			display: flex;
			align-items: center;
			justify-content: space-between;
			padding-bottom: 16px;
			border-bottom: 1px solid rgba(255,255,255,0.06);
			margin-bottom: 20px;
		}
		.session-title {
			font-size: 22px;
			font-weight: 800;
			color: var(--accent);
		}
		.status-badge {
			padding: 6px 12px;
			border-radius: 30px;
			font-size: 12px;
			font-weight: 700;
			letter-spacing: 0.05em;
		}
		.status-badge.MET { background: rgba(76,175,80,0.15); color: #81c784; border: 1px solid rgba(76,175,80,0.3); }
		.status-badge.PENDING { background: rgba(255,152,0,0.15); color: #ffb74d; border: 1px solid rgba(255,152,0,0.3); }
		.status-badge.BLOCKED { background: rgba(244,67,54,0.15); color: #e57373; border: 1px solid rgba(244,67,54,0.3); }
		.status-badge.EXPIRED { background: rgba(158,158,158,0.15); color: #e0e0e0; border: 1px solid rgba(158,158,158,0.3); }
		
		.tally-grid {
			display: grid;
			grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
			gap: 16px;
			margin-bottom: 24px;
		}
		.tally-item {
			padding: 16px;
			background: rgba(255,255,255,0.02);
			border-radius: 12px;
			border: 1px solid rgba(255,255,255,0.04);
		}
		.tally-actor {
			font-weight: 700;
			margin-bottom: 8px;
			display: flex;
			align-items: center;
			justify-content: space-between;
		}
		.tally-verdict {
			font-size: 14px;
			padding: 2px 8px;
			border-radius: 4px;
			font-weight: 600;
			text-transform: uppercase;
		}
		.verdict-approve { background: rgba(76,175,80,0.15); color: #81c784; }
		.verdict-reject { background: rgba(244,67,54,0.15); color: #e57373; }
		.verdict-request_changes { background: rgba(255,152,0,0.15); color: #ffb74d; }
		.tally-sha {
			font-family: monospace;
			font-size: 13px;
			color: var(--text-muted);
			margin-top: 6px;
		}
		
		.actions {
			display: flex;
			gap: 12px;
			margin-top: 20px;
			flex-wrap: wrap;
		}
		.btn {
			padding: 10px 20px;
			border-radius: 10px;
			border: none;
			font-weight: 600;
			cursor: pointer;
			transition: all 0.2s ease;
			font-family: inherit;
		}
		.btn-approve { background: #4caf50; color: #fff; }
		.btn-approve:hover { background: #66bb6a; box-shadow: 0 0 15px rgba(76,175,80,0.4); }
		.btn-reject { background: #f44336; color: #fff; }
		.btn-reject:hover { background: #ef5350; box-shadow: 0 0 15px rgba(244,67,54,0.4); }
		.btn-request { background: #ff9800; color: #fff; }
		.btn-request:hover { background: #ffa726; box-shadow: 0 0 15px rgba(255,152,0,0.4); }
		
		.comment-box {
			margin-top: 24px;
			display: flex;
			gap: 12px;
		}
		.comment-input {
			flex: 1;
			background: rgba(0,0,0,0.2);
			border: 1px solid var(--border-color);
			border-radius: 10px;
			padding: 12px;
			color: var(--text-main);
			font-family: inherit;
			outline: none;
			transition: border-color 0.2s ease;
		}
		.comment-input:focus {
			border-color: var(--accent);
		}
		.btn-send { background: var(--accent); color: #fff; }
		.btn-send:hover { background: #ff7043; box-shadow: var(--accent-glow); }
	</style>
</head>
<body>
	<header>
		<h1>botfam Operator Console</h1>
		<div style="font-size: 14px; color: var(--text-muted)">Host: localhost</div>
	</header>
	<div class="container">
		<div class="sidebar">
			<div class="card">
				<h2>Roster & Presence</h2>
				<div id="roster" class="roster-list">Loading...</div>
			</div>
		</div>
		<div class="main-content">
			<div class="card" style="flex: 1;">
				<h2>CCREP deliberative sessions</h2>
				<div id="sessions">Loading sessions...</div>
			</div>
		</div>
	</div>

	<script>
		let familyData = {};

		async function refreshData() {
			try {
				const r = await fetch('/ui/data');
				familyData = await r.json();
				renderUI();
			} catch(e) {
				console.error("error fetching UI data", e);
			}
		}

		function renderUI() {
			const rosterDiv = document.getElementById('roster');
			const sessionsDiv = document.getElementById('sessions');

			const familyKeys = Object.keys(familyData);
			if (familyKeys.length === 0) {
				rosterDiv.innerHTML = "<div>No active family registry found.</div>";
				sessionsDiv.innerHTML = "<div>No active sessions.</div>";
				return;
			}

			const familyRoot = familyKeys[0];
			const family = familyData[familyRoot];

			let rosterHtml = "";
			family.roster.forEach(actor => {
				const status = family.presence[actor] || "offline";
				rosterHtml += _BACKTICK_
					<div class="roster-item">
						<span class="actor-name">${actor}</span>
						<span class="presence-badge">
							<span class="dot ${status}"></span>
							${status}
						</span>
					</div>
				_BACKTICK_;
			});
			rosterDiv.innerHTML = rosterHtml;

			let sessionsHtml = "";
			if (!family.sessions || family.sessions.length === 0) {
				sessionsHtml = "<div>No active sessions found. Run <code>botfam session new &lt;name&gt;</code> to start one.</div>";
			} else {
				family.sessions.forEach(sess => {
					let tallyHtml = "";
					let votesList = [];
					if (sess.tally && sess.tally.votes) {
						Object.keys(sess.tally.votes).forEach(actor => {
							const v = sess.tally.votes[actor];
							votesList.push(v);
						});
					}

					if (votesList.length > 0) {
						tallyHtml = '<div class="tally-grid">';
						votesList.forEach(v => {
							tallyHtml += _BACKTICK_
								<div class="tally-item">
									<div class="tally-actor">
										<span>${v.actor}</span>
										<span class="tally-verdict verdict-${v.verdict}">${v.verdict}</span>
									</div>
									<div class="tally-sha">commit: ${v.commit_sha ? v.commit_sha.substring(0,7) : 'n/a'}</div>
									<div style="font-size: 11px; color: var(--text-muted); margin-top: 4px;">via ${v.provenance}</div>
								</div>
							_BACKTICK_;
						});
						tallyHtml += '</div>';
					} else {
						tallyHtml = "<div style='color: var(--text-muted); margin-bottom: 20px;'>No votes cast yet.</div>";
					}

					sessionsHtml += _BACKTICK_
						<div class="session-card" style="padding-bottom: 24px; border-bottom: 1px solid rgba(255,255,255,0.05); margin-bottom: 24px;">
							<div class="session-header">
								<span class="session-title">${sess.slug}</span>
								<span class="status-badge ${sess.status}">${sess.status}</span>
							</div>
							<div style="font-size: 14px; margin-bottom: 16px;">
								<strong>Author:</strong> ${sess.tally?.author || 'n/a'} | 
								<strong>Latest Commit:</strong> <code style="background: rgba(0,0,0,0.2); padding: 2px 6px; border-radius: 4px;">${sess.tally?.latest_sha || 'n/a'}</code>
							</div>
							
							${tallyHtml}

							<div class="actions">
								<button class="btn btn-approve" onclick="castVote('${familyRoot}', '${sess.slug}', 'approve')">Approve</button>
								<button class="btn btn-request" onclick="castVote('${familyRoot}', '${sess.slug}', 'request_changes')">Request Changes</button>
								<button class="btn btn-reject" onclick="castVote('${familyRoot}', '${sess.slug}', 'reject')">Reject/Veto</button>
							</div>

							<div class="comment-box">
								<input type="text" id="comment-${sess.slug}" class="comment-input" placeholder="Type a comment or instruction...">
								<button class="btn btn-send" onclick="sendComment('${familyRoot}', '${sess.slug}')">Post Comment</button>
							</div>
						</div>
					_BACKTICK_;
				});
			}
			sessionsDiv.innerHTML = sessionsHtml;
		}

		async function castVote(workDir, proposalID, verdict) {
			try {
				const r = await fetch('/ui/vote', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ work_dir: workDir, proposal_id: proposalID, verdict })
				});
				if (r.ok) {
					refreshData();
				} else {
					const err = await r.json();
					alert("Failed to vote: " + err.error);
				}
			} catch(e) {
				alert("Error voting: " + e);
			}
		}

		async function sendComment(workDir, session) {
			const input = document.getElementById("comment-" + session);
			const body = input.value;
			if (!body) return;

			try {
				const r = await fetch('/ui/comment', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ work_dir: workDir, session: session, body: body })
				});
				if (r.ok) {
					input.value = "";
					refreshData();
				} else {
					const err = await r.json();
					alert("Failed to post comment: " + err.error);
				}
			} catch(e) {
				alert("Error posting comment: " + e);
			}
		}

		refreshData();

		const ev = new EventSource('/ui/events');
		ev.onmessage = function(e) {
			console.log("SSE Event:", e.data);
			refreshData();
		};
	</script>
</body>
</html>`
	html = strings.ReplaceAll(html, "_BACKTICK_", "`")
	_, _ = w.Write([]byte(html))
}

func (s *Server) handleUIEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan string, 10)
	clientID := fmt.Sprintf("client-%d", time.Now().UnixNano())

	s.clientsMu.Lock()
	s.clients[clientID] = ch
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, clientID)
		s.clientsMu.Unlock()
	}()

	// Send initial connected ping
	_, _ = fmt.Fprintf(w, "data: connected\n\n")
	flusher.Flush()

	for {
		select {
		case msg := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcastEvent(event string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for _, ch := range s.clients {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Server) ensureActorLock(fs *FamilyState, actor string, pid int) error {
	if fs.locks[actor] != nil {
		ownerPID := fs.lockPIDs[actor]
		if ownerPID == pid && pid > 0 {
			return nil
		}
		// If PID is different, check if owner process is still alive
		if ownerPID > 0 && isProcessAlive(ownerPID) {
			return fmt.Errorf("actor %q is locked by PID %d", actor, ownerPID)
		}
		// Owner is dead! Release lock and acquire fresh
		_ = fs.locks[actor].Close()
		delete(fs.locks, actor)
		delete(fs.lockPIDs, actor)
	}
	lock, err := fs.store.LockActor(actor)
	if err != nil {
		return err
	}
	fs.locks[actor] = lock
	fs.lockPIDs[actor] = pid
	return fs.store.RollbackProcessing(actor)
}

func gitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func (s *Server) validateRequestActor(r *http.Request, reqActor string, reqWorkDir string) error {
	if os.Getenv("BOTFAM_TESTING") == "1" {
		return nil
	}

	conn, ok := r.Context().Value(connKey).(net.Conn)
	if !ok {
		if reqActor == "operator" {
			return nil
		}
		return fmt.Errorf("non-UDS connection not allowed for actor %q", reqActor)
	}

	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		if reqActor == "operator" {
			return nil
		}
		return fmt.Errorf("non-unix connection not allowed for actor %q", reqActor)
	}

	pid, err := getPeerPID(unixConn)
	if err != nil {
		return fmt.Errorf("failed to get peer PID: %w", err)
	}

	cwd, err := getProcessCWD(pid)
	if err != nil {
		return fmt.Errorf("failed to get process CWD for PID %d: %w", pid, err)
	}

	gitRoot, err := findGitRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to find git root for CWD %q: %w", cwd, err)
	}

	// Resolve the main repository root
	mainGitRoot := gitRoot
	commonDir, err := gitCommand(gitRoot, "rev-parse", "--git-common-dir")
	if err == nil {
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Clean(filepath.Join(gitRoot, commonDir))
		}
		mainGitRoot = filepath.Dir(commonDir)
	}


	evalMainGitRoot, err := filepath.EvalSymlinks(mainGitRoot)
	if err != nil {
		evalMainGitRoot = mainGitRoot
	}
	evalReqWorkDir, err := filepath.EvalSymlinks(reqWorkDir)
	if err != nil {
		evalReqWorkDir = reqWorkDir
	}

	matched := false
	if evalReqWorkDir == evalMainGitRoot {
		matched = true
	} else {
		// Check if it's the ~/.botfam store path
		rootsStr, err := gitCommand(gitRoot, "rev-list", "--max-parents=0", "HEAD")
		if err == nil {
			lines := strings.Split(strings.TrimSpace(rootsStr), "\n")
			var cleanLines []string
			for _, l := range lines {
				if l != "" {
					cleanLines = append(cleanLines, l)
				}
			}
			sort.Strings(cleanLines)
			sum := sha256.Sum256([]byte(strings.Join(cleanLines, "\n")))
			id := hex.EncodeToString(sum[:])[:12]
			if strings.Contains(evalReqWorkDir, "fam-"+id) {
				matched = true
			}
		}
	}

	if !matched {
		return fmt.Errorf("work_dir mismatch: resolved git root %q does not match requested work_dir %q", evalMainGitRoot, evalReqWorkDir)
	}

	resolvedActor := actorFromDir(gitRoot)

	isAgent := func(name string) bool {
		return name == "agy" || name == "claude" || name == "codex" || name == "antigravity" ||
			name == "alice" || name == "bob" || name == "charlie"
	}

	if reqActor == "operator" {
		if isAgent(resolvedActor) {
			return fmt.Errorf("operator cannot perform actions from agent worktree %q (resolved actor %q)", gitRoot, resolvedActor)
		}
	} else {
		normReq := strings.TrimPrefix(reqActor, "wt-")
		normReq = strings.TrimPrefix(normReq, "botfam-")
		normResolved := strings.TrimPrefix(resolvedActor, "wt-")
		normResolved = strings.TrimPrefix(normResolved, "botfam-")

		if normReq == "antigravity" {
			normReq = "agy"
		}
		if normResolved == "antigravity" {
			normResolved = "agy"
		}

		if normReq != normResolved {
			return fmt.Errorf("actor-worktree mismatch: actor %q cannot perform actions from worktree %q (resolved actor %q)", reqActor, gitRoot, resolvedActor)
		}
	}

	return nil
}

func actorFromDir(path string) string {
	base := filepath.Base(path)
	base = strings.TrimPrefix(base, "wt-")
	base = strings.TrimPrefix(base, "botfam-")
	return base
}

func getRequestPID(r *http.Request) int {
	if conn, ok := r.Context().Value(connKey).(net.Conn); ok {
		if unixConn, ok := conn.(*net.UnixConn); ok {
			if pid, err := getPeerPID(unixConn); err == nil {
				return pid
			}
		}
	}
	return 0
}

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EPERM {
		return true
	}
	return false
}

type uiFamilyStateInfo struct {
	Roster   []string                 `json:"roster"`
	Presence map[string]string        `json:"presence"`
	Sessions []uiSessionInfo          `json:"sessions"`
}

type uiSessionInfo struct {
	Slug   string         `json:"slug"`
	Status string         `json:"status"`
	Tally  *uiTallyResult `json:"tally"`
}

func getRoster(root string) ([]string, error) {
	files, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var roster []string
	for _, f := range files {
		if !f.IsDir() || f.Name() == "tmp" || f.Name() == "tasks" || f.Name() == "sessions" || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		roster = append(roster, f.Name())
	}
	sort.Strings(roster)
	return roster, nil
}

func (s *Server) handleUIData(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	familiesCopy := make(map[string]*FamilyState)
	for k, v := range s.families {
		familiesCopy[k] = v
	}
	s.mu.Unlock()

	data := make(map[string]uiFamilyStateInfo)

	for root, fs := range familiesCopy {
		fs.mu.Lock()
		roster, err := getRoster(fs.store.RootPath())
		if err != nil {
			fs.mu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		presence := make(map[string]string)
		for _, actor := range roster {
			status := "offline"
			if lastSeen, ok := fs.activeActors[actor]; ok {
				if time.Since(lastSeen) <= 5*time.Minute {
					status = "online"
				} else if time.Since(lastSeen) <= 30*time.Minute {
					status = "away"
				}
			}
			if fs.locks[actor] != nil {
				ownerPID := fs.lockPIDs[actor]
				if ownerPID > 0 && isProcessAlive(ownerPID) {
					status = "online"
				}
			}
			presence[actor] = status
		}

		activeSessions, err := fs.store.SessionList()
		if err != nil {
			fs.mu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		var sessions []uiSessionInfo
		for _, sess := range activeSessions {
			tally, err := s.computeTallyInternal(fs, sess.Slug)
			if err != nil {
				tally = &uiTallyResult{
					ProposalID: sess.Slug,
					Status:     "PENDING",
					Votes:      map[string]uiVoteInfo{},
				}
			}
			sessions = append(sessions, uiSessionInfo{
				Slug:   sess.Slug,
				Status: tally.Status,
				Tally:  tally,
			})
		}
		fs.mu.Unlock()

		data[root] = uiFamilyStateInfo{
			Roster:   roster,
			Presence: presence,
			Sessions: sessions,
		}
	}

	writeJSON(w, data)
}

func (s *Server) handleUIVote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir    string `json:"work_dir"`
		ProposalID string `json:"proposal_id"`
		Verdict    string `json:"verdict"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	tally, err := s.computeTallyInternal(fs, req.ProposalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tally.LatestSHA == "" {
		writeError(w, http.StatusBadRequest, "no active proposal commit SHA found")
		return
	}

	if err := s.ensureActorLock(fs, "operator", os.Getpid()); err != nil {
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	bodyMap := map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": req.ProposalID,
		"commit_sha":  tally.LatestSHA,
		"verdict":     req.Verdict,
		"reviewer":    "operator",
	}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	entry, err := fs.store.SessionAppend(req.ProposalID, "operator", string(bodyBytes), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Register operator's vote in the daemon's in-memory registry
	s.mu.Lock()
	if _, ok := s.votes[req.ProposalID]; !ok {
		s.votes[req.ProposalID] = make(map[string]*voteConnection)
	}
	s.votes[req.ProposalID]["operator"] = &voteConnection{
		proposalID: req.ProposalID,
		actor:      "operator",
		verdict:    req.Verdict,
		commitSHA:  tally.LatestSHA,
	}
	s.mu.Unlock()

	s.broadcastEvent(fmt.Sprintf("session:append:%s", req.ProposalID))
	writeJSON(w, entry)
}

func (s *Server) handleUIComment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkDir string `json:"work_dir"`
		Session string `json:"session"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fs, err := s.getFamily(req.WorkDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if err := s.ensureActorLock(fs, "operator", os.Getpid()); err != nil {
		writeError(w, http.StatusLocked, err.Error())
		return
	}

	entry, err := fs.store.SessionAppend(req.Session, "operator", req.Body, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.broadcastEvent(fmt.Sprintf("session:append:%s", req.Session))
	writeJSON(w, entry)
}
