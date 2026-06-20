package ops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	contractconnect "github.com/robertolupi/botfam/internal/eventdelivery/contract/connect"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/emptypb"
)

// NewSprintCmd builds the `botfam sprint` Cobra command and its subcommands.
func NewSprintCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "sprint",
		Short:         "Manage sprint sessions (M1 skeleton)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	c.AddCommand(newSprintStartCmd())
	c.AddCommand(newSprintRunCmd())
	c.AddCommand(newSprintEndCmd())
	c.AddCommand(newSprintLsCmd())
	c.AddCommand(newSprintUiCmd())

	return c
}

func newSprintStartCmd() *cobra.Command {
	var milestone int64
	var issuesStr string

	cmd := &cobra.Command{
		Use:   "start $ID",
		Short: "Start a sprint session with a milestone or specific issues",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if milestone == 0 && issuesStr == "" {
				return errors.New("must specify either --milestone N or --issues N1,N2")
			}

			var issues []string
			if issuesStr != "" {
				issues = strings.Split(issuesStr, ",")
				for i, issue := range issues {
					issues[i] = strings.TrimSpace(issue)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sprint start placeholder: ID=%s, Milestone=%d, Issues=%v\n", id, milestone, issues)
			return nil
		},
	}

	cmd.Flags().Int64Var(&milestone, "milestone", 0, "Milestone number")
	cmd.Flags().StringVar(&issuesStr, "issues", "", "Comma-separated list of issue numbers")
	return cmd
}

func newSprintRunCmd() *cobra.Command {
	var workerCommand string
	var workerTTL time.Duration

	cmd := &cobra.Command{
		Use:   "run $ID",
		Short: "Run the sprint supervisor (acquires lease)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoName := famconfig.ResolveRepoName(wd)
			if repoName == "" {
				return errors.New("could not resolve repository name from current directory")
			}

			// Determine session dir: ~/.botfam/sessions/<ID>
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("user home dir: %w", err)
			}
			sessionDir := filepath.Join(home, ".botfam", "sessions", id)
			if err := os.MkdirAll(sessionDir, 0o755); err != nil {
				return fmt.Errorf("create session dir: %w", err)
			}

			// Run ID recovery
			lastRunNumber, err := getLastRunNumber(sessionDir)
			if err != nil {
				return fmt.Errorf("failed to recover last run number: %w", err)
			}

			lease := singlehost.NewLease()
			defer lease.Close()

			grant, err := lease.Acquire(cmd.Context(), &connect.Request[pb.AcquireRequest]{
				Msg: &pb.AcquireRequest{
					Scope: &pb.Scope{
						RepoName: repoName,
					},
					HolderIdentity: "supervisor",
				},
			})
			if err != nil {
				return fmt.Errorf("lease acquisition failed: %w", err)
			}

			if !grant.Msg.GetGranted() {
				return fmt.Errorf("sprint run: lease is busy or held by another live process")
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sprint run started: lease acquired for session %s (fencing_token=%d)\n", id, grant.Msg.GetFencingToken())

			if cmd.Context().Err() != nil {
				_, _ = lease.Release(context.Background(), &connect.Request[pb.ReleaseRequest]{
					Msg: &pb.ReleaseRequest{
						LeaseId: grant.Msg.GetLeaseId(),
					},
				})
				return nil
			}

			// Initialize session repo as git repository if it isn't one already
			if _, err := os.Stat(filepath.Join(sessionDir, ".git")); os.IsNotExist(err) {
				initCmd := exec.CommandContext(cmd.Context(), "git", "init")
				initCmd.Dir = sessionDir
				if out, err := initCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed to git init session directory: %w: %s", err, string(out))
				}
				// Configure dummy user/email locally to prevent crash-recovery commit failures
				configUserCmd := exec.CommandContext(cmd.Context(), "git", "config", "user.name", "botfam-supervisor")
				configUserCmd.Dir = sessionDir
				_ = configUserCmd.Run()
				configEmailCmd := exec.CommandContext(cmd.Context(), "git", "config", "user.email", "supervisor@botfam.invalid")
				configEmailCmd.Dir = sessionDir
				_ = configEmailCmd.Run()
			}

			// Open session repo
			db, err := store.OpenSessionRepo(cmd.Context(), store.SessionRepoOptions{
				Dir:               sessionDir,
				RunNumber:         lastRunNumber,
				GitignorePatterns: singlehost.SessionRepoGitignorePatterns(),
			})
			if err != nil {
				return fmt.Errorf("failed to open session repository: %w", err)
			}
			defer db.Close()

			// Mark any previously running runs as crashed
			_, _ = db.ExecContext(cmd.Context(), `UPDATE runs SET status = 'crashed', completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE status = 'running'`)

			// Insert current run
			currentRunID := fmt.Sprintf("run-%d", lastRunNumber+1)
			_, err = db.ExecContext(cmd.Context(), `INSERT INTO runs (id, session_id, status) VALUES (?, ?, 'running')`, currentRunID, id)
			if err != nil {
				return fmt.Errorf("failed to insert current run: %w", err)
			}

			// Connect server setup
			socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("bf-%s.sock", id))
			mux := http.NewServeMux()

			wcHandler := &supervisorWorkerChannel{db: db}
			wcPath, wcMux := contractconnect.NewWorkerChannelHandler(wcHandler)
			mux.Handle(wcPath, wcMux)

			sessionToken := uuid.New().String()
			resolverHandler := &supervisorSessionResolver{
				sessionID:    id,
				fencingToken: grant.Msg.GetFencingToken(),
				addr:         socketPath,
				token:        sessionToken,
			}
			resPath, resMux := contractconnect.NewSessionResolverHandler(resolverHandler)
			mux.Handle(resPath, resMux)

			server, err := singlehost.Serve(cmd.Context(), socketPath, mux)
			if err != nil {
				return fmt.Errorf("failed to start supervisor Connect server: %w", err)
			}
			defer server.HTTPServer.Close()

			// Set the actual endpoint and token in lease session file
			if err := lease.SetEndpoint(socketPath, sessionToken); err != nil {
				return fmt.Errorf("failed to update lease session file: %w", err)
			}

			// Supervisor execution loop
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

			type activeWorker struct {
				workerID   string
				workItemID string
				cmd        *exec.Cmd
				startTime  time.Time
				ttl        time.Duration
			}
			type workerResult struct {
				workerID   string
				workItemID string
				err        error
			}

			workers := make(map[string]*activeWorker)
			resultChan := make(chan workerResult, 10)

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			runSupervisor := true
			for runSupervisor {
				select {
				case <-cmd.Context().Done():
					runSupervisor = false
				case <-sigChan:
					runSupervisor = false
				case res := <-resultChan:
					_, ok := workers[res.workerID]
					if ok {
						delete(workers, res.workerID)
						completedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
						if res.err == nil {
							_, _ = db.ExecContext(cmd.Context(), `UPDATE work_items SET state = 'completed' WHERE id = ?`, res.workItemID)
							_, _ = db.ExecContext(cmd.Context(), `UPDATE dispatches SET completed_at = ? WHERE work_item_id = ? AND worker_id = ?`, completedAt, res.workItemID, res.workerID)
							_, _ = db.ExecContext(cmd.Context(), `INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, 'running', 'completed', 'Completed successfully')`, res.workItemID)
						} else {
							_, _ = db.ExecContext(cmd.Context(), `UPDATE work_items SET state = 'failed' WHERE id = ?`, res.workItemID)
							_, _ = db.ExecContext(cmd.Context(), `UPDATE dispatches SET completed_at = ? WHERE work_item_id = ? AND worker_id = ?`, completedAt, res.workItemID, res.workerID)
							reason := fmt.Sprintf("Worker failed: %v", res.err)
							_, _ = db.ExecContext(cmd.Context(), `INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, 'running', 'failed', ?)`, res.workItemID, reason)
						}
					}
				case <-ticker.C:
					// Check TTL timeouts
					now := time.Now()
					for wid, w := range workers {
						if now.Sub(w.startTime) > w.ttl {
							_ = w.cmd.Process.Signal(syscall.SIGTERM)
							time.AfterFunc(500*time.Millisecond, func() {
								_ = w.cmd.Process.Kill()
							})
							delete(workers, wid)
							completedAt := now.UTC().Format("2006-01-02T15:04:05.000Z")
							_, _ = db.ExecContext(cmd.Context(), `UPDATE work_items SET state = 'failed' WHERE id = ?`, w.workItemID)
							_, _ = db.ExecContext(cmd.Context(), `UPDATE dispatches SET completed_at = ? WHERE work_item_id = ? AND worker_id = ?`, completedAt, w.workItemID, w.workerID)
							_, _ = db.ExecContext(cmd.Context(), `INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, 'running', 'failed', 'Worker timed out')`, w.workItemID)
						}
					}

					// Poll for pending work items
					if workerCommand != "" {
						rows, err := db.QueryContext(cmd.Context(), `SELECT id, scope_generation FROM work_items WHERE state = 'pending'`)
						if err == nil {
							var workItemsToDispatch []struct {
								id       string
								scopeGen int
							}
							for rows.Next() {
								var wid string
								var sgen int
								if rows.Scan(&wid, &sgen) == nil {
									workItemsToDispatch = append(workItemsToDispatch, struct {
										id       string
										scopeGen int
									}{wid, sgen})
								}
							}
							rows.Close()

							for _, wi := range workItemsToDispatch {
								workerID := fmt.Sprintf("worker-%s", uuid.New().String()[:8])
								_, _ = db.ExecContext(cmd.Context(), `UPDATE work_items SET state = 'running' WHERE id = ?`, wi.id)
								_, _ = db.ExecContext(cmd.Context(), `INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, 'pending', 'running', 'Dispatched')`, wi.id)
								dispatchID := uuid.New().String()
								_, _ = db.ExecContext(cmd.Context(), `INSERT INTO dispatches (id, work_item_id, worker_id, scope_generation) VALUES (?, ?, ?, ?)`, dispatchID, wi.id, workerID, wi.scopeGen)

								parts := strings.Fields(workerCommand)
								wCmd := exec.CommandContext(cmd.Context(), parts[0], parts[1:]...)
								wCmd.Dir = wd
								wCmd.Stdout = cmd.OutOrStdout()
								wCmd.Stderr = cmd.OutOrStderr()

								_, _, traceparentVal := getOrGenerateTraceparent()
								wCmd.Env = append(os.Environ(),
									"TRACEPARENT="+traceparentVal,
									"BOTFAM_WORKER_ID="+workerID,
									"BOTFAM_WORK_ITEM_ID="+wi.id,
									"BOTFAM_WORKER_CHANNEL_SOCKET="+socketPath,
									"BOTFAM_FENCING_TOKEN="+strconv.FormatUint(grant.Msg.GetFencingToken(), 10),
								)


								if err := wCmd.Start(); err == nil {
									workers[workerID] = &activeWorker{
										workerID:   workerID,
										workItemID: wi.id,
										cmd:        wCmd,
										startTime:  now,
										ttl:        workerTTL,
									}
									go func(wid string, wiid string, c *exec.Cmd) {
										resultChan <- workerResult{workerID: wid, workItemID: wiid, err: c.Wait()}
									}(workerID, wi.id, wCmd)
								} else {
									completedAt := now.UTC().Format("2006-01-02T15:04:05.000Z")
									_, _ = db.ExecContext(cmd.Context(), `UPDATE work_items SET state = 'failed' WHERE id = ?`, wi.id)
									_, _ = db.ExecContext(cmd.Context(), `UPDATE dispatches SET completed_at = ? WHERE work_item_id = ? AND worker_id = ?`, completedAt, wi.id, workerID)
									_, _ = db.ExecContext(cmd.Context(), `INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, 'running', 'failed', ?)`, wi.id, fmt.Sprintf("Failed to start worker: %v", err))
								}
							}
						}
					}
				}
			}

			// Clean up active workers on exit
			for _, w := range workers {
				_ = w.cmd.Process.Kill()
			}

			// Mark run completed
			_, _ = db.ExecContext(context.Background(), `UPDATE runs SET status = 'completed', completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, currentRunID)

			// Clean release
			_, _ = lease.Release(context.Background(), &connect.Request[pb.ReleaseRequest]{
				Msg: &pb.ReleaseRequest{
					LeaseId: grant.Msg.GetLeaseId(),
				},
			})

			fmt.Fprintln(cmd.OutOrStdout(), "Sprint run stopped.")
			return nil
		},
	}

	cmd.Flags().StringVar(&workerCommand, "worker-command", "", "The executable or command to spawn a worker subprocess")
	cmd.Flags().DurationVar(&workerTTL, "worker-ttl", 30*time.Second, "The timeout duration for spawned worker subprocesses")
	return cmd
}

func getLastRunNumber(sessionDir string) (int, error) {
	dbPath := filepath.Join(sessionDir, "session.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return 0, nil
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var tableExists int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'runs'`).Scan(&tableExists)
	if err != nil || tableExists == 0 {
		return 0, nil
	}

	rows, err := db.Query(`SELECT id FROM runs`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	maxNum := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			var num int
			if _, err := fmt.Sscanf(id, "run-%d", &num); err == nil {
				if num > maxNum {
					maxNum = num
				}
			}
		}
	}
	return maxNum, nil
}

func generateTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func getOrGenerateTraceparent() (traceID string, spanID string, traceparentVal string) {
	tp := os.Getenv("TRACEPARENT")
	if tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) >= 4 && parts[0] == "00" {
			traceID = parts[1]
			childSpanID := generateSpanID()
			return traceID, childSpanID, fmt.Sprintf("00-%s-%s-%s", traceID, childSpanID, parts[3])
		}
	}
	traceID = generateTraceID()
	childSpanID := generateSpanID()
	return traceID, childSpanID, fmt.Sprintf("00-%s-%s-01", traceID, childSpanID)
}

type supervisorWorkerChannel struct {
	db *sql.DB
}

func (s *supervisorWorkerChannel) DispatchWork(ctx context.Context, req *connect.Request[pb.WorkerStream], stream *connect.ServerStream[pb.WorkItem]) error {
	return nil
}

func (s *supervisorWorkerChannel) RecordArtifact(ctx context.Context, req *connect.Request[pb.Artifact]) (*connect.Response[emptypb.Empty], error) {
	art := req.Msg
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO artifacts (id, work_item_id, kind, uri, sha256) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(kind, uri, sha256) DO NOTHING`,
		uuid.New().String(), art.GetWorkItemId(), art.GetKind(), art.GetUri(), art.GetSha256())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *supervisorWorkerChannel) SubmitTelemetry(ctx context.Context, stream *connect.ClientStream[pb.Span]) (*connect.Response[emptypb.Empty], error) {
	for stream.Receive() {
		span := stream.Msg()
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO git_action_log (id, run_id, action, trace_id, span_id, payload_json)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), "", span.GetEventType(), span.GetTraceId(), span.GetSpanId(), span.GetPayloadJson())
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s *supervisorWorkerChannel) ProposeForgeAction(ctx context.Context, req *connect.Request[pb.ForgeAction]) (*connect.Response[pb.ActionAck], error) {
	action := req.Msg
	res, err := store.EnqueueForgeAction(ctx, s.db, uuid.New().String(), action.GetWorkItemId(), action.GetActionKey(), action.GetToolName(), action.GetArgumentsJson(), 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.ActionAck{
		Committed: true,
		Deduped:   res.Deduped,
		OutboxId:  res.ID,
	}), nil
}

type supervisorSessionResolver struct {
	sessionID    string
	fencingToken uint64
	addr         string
	token        string
}

func (s *supervisorSessionResolver) Resolve(ctx context.Context, req *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error) {
	return connect.NewResponse(&pb.SessionEndpoint{
		Found:        true,
		Address:      s.addr,
		Token:        s.token,
		SessionId:    s.sessionID,
		FencingToken: s.fencingToken,
	}), nil
}


func newSprintEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "end $ID",
		Short: "End a sprint session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			fmt.Fprintf(cmd.OutOrStdout(), "Sprint end placeholder: ID=%s\n", id)
			return nil
		},
	}
}

func newSprintLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sprint sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "Sprint list placeholder")
			return nil
		},
	}
}

func newSprintUiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui $ID",
		Short: "Inspect a running or past session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			fmt.Fprintf(cmd.OutOrStdout(), "Sprint UI placeholder: ID=%s\n", id)
			return nil
		},
	}
}
