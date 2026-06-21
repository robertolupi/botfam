package ops

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
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
	"github.com/robertolupi/botfam/internal/eventdelivery/workerchannel"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mcp"
	"github.com/spf13/cobra"
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
			if milestone != 0 && issuesStr != "" {
				return errors.New("specify only one of --milestone or --issues")
			}
			issues, err := parseIssueNumbers(issuesStr)
			if err != nil {
				return err
			}

			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoName := famconfig.ResolveRepoName(wd)
			if repoName == "" {
				return errors.New("could not resolve repository name from current directory")
			}
			fctx, err := famctx.ResolveAgentRuntime(wd)
			if err != nil {
				return fmt.Errorf("resolve agent runtime context: %w", err)
			}
			client, err := forge.NewClientForWorkDir(wd, fctx.Actor)
			if err != nil {
				return fmt.Errorf("forge client: %w", err)
			}

			sessionDir, err := sprintSessionDir(id)
			if err != nil {
				return err
			}

			genID, members, err := runSprintStart(cmd.Context(), client, sessionDir, id, repoName, milestone, issues)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sprint %s started: scope generation %d seeded with %d work item(s) at %s\n", id, genID, len(members), sessionDir)
			return nil
		},
	}

	cmd.Flags().Int64Var(&milestone, "milestone", 0, "Milestone number")
	cmd.Flags().StringVar(&issuesStr, "issues", "", "Comma-separated list of issue numbers")
	return cmd
}

// sprintIssueClient is the minimal forge surface `sprint start` needs to resolve
// a scope into concrete issues. Satisfied by *forge.Client; faked in tests.
type sprintIssueClient interface {
	GetIssue(ctx context.Context, num int) (*forge.Issue, error)
	ListIssuesByMilestone(ctx context.Context, milestoneID int64) ([]*forge.Issue, error)
}

// scopeIssue is a resolved scope member: an issue number and its title.
type scopeIssue struct {
	number int
	title  string
}

// sprintSessionDir returns the canonical session directory ~/.botfam/sessions/<id>.
func sprintSessionDir(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".botfam", "sessions", id), nil
}

// parseIssueNumbers parses a comma-separated list of positive issue numbers.
func parseIssueNumbers(s string) ([]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid issue number %q", part)
		}
		out = append(out, n)
	}
	return out, nil
}

// ensureSessionGitRepo creates the session directory and initializes it as a git
// repo (with a local identity) if it is not one already. `sprint start` owns
// session creation; `sprint run` requires the session to already exist.
func ensureSessionGitRepo(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	runner := store.ExecRunner{}
	if out, err := runner.Run(ctx, dir, "git", "init"); err != nil {
		return fmt.Errorf("git init session dir: %w: %s", err, string(out))
	}
	_, _ = runner.Run(ctx, dir, "git", "config", "user.name", "botfam-supervisor")
	_, _ = runner.Run(ctx, dir, "git", "config", "user.email", "supervisor@botfam.invalid")
	return nil
}

// createSessionRepo initializes a fresh session repository end-to-end: a git repo
// carrying the session identity, the artifacts dir, the gitignore, and an opened,
// migrated session.db. It is the create half of the session-store lifecycle; the
// open-existing half (with crashed-run recovery) is store.OpenSessionRepo, used by
// `sprint run`. `start` creates, so there is no prior run to recover. The caller
// owns the returned *sql.DB and must Close it.
func createSessionRepo(ctx context.Context, sessionDir string) (*sql.DB, error) {
	if err := ensureSessionGitRepo(ctx, sessionDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "artifacts"), 0o755); err != nil {
		return nil, fmt.Errorf("create artifacts dir: %w", err)
	}
	if err := store.EnsureSessionGitignore(sessionDir, singlehost.SessionRepoGitignorePatterns()...); err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}
	if err := store.ApplyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return db, nil
}

// runSprintStart resolves the scope, creates/opens the session store, and seeds a
// new scope generation with its in-scope membership and one pending work item per
// issue. Each call advances to a fresh scope generation (the design's
// scope-as-snapshot model); the work_items UNIQUE(kind, source_id,
// scope_generation) constraint dedups within a generation.
func runSprintStart(ctx context.Context, client sprintIssueClient, sessionDir, sessionID, repoName string, milestone int64, issues []int) (int64, []scopeIssue, error) {
	members, sourceQuery, err := resolveScopeMembers(ctx, client, milestone, issues)
	if err != nil {
		return 0, nil, err
	}
	if len(members) == 0 {
		return 0, nil, errors.New("sprint start: resolved scope is empty")
	}

	db, err := createSessionRepo(ctx, sessionDir)
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()

	genID, err := seedScopeGeneration(ctx, db, repoName, milestone, sourceQuery, members)
	if err != nil {
		return 0, nil, err
	}

	msg := fmt.Sprintf("sprint start %s: scope generation %d (%d issues)", sessionID, genID, len(members))
	if err := commitSessionSnapshot(ctx, sessionDir, msg); err != nil {
		return 0, nil, err
	}
	return genID, members, nil
}

// resolveScopeMembers turns a --milestone or --issues selection into concrete
// (number, title) members plus a recorded source query.
func resolveScopeMembers(ctx context.Context, client sprintIssueClient, milestone int64, issues []int) ([]scopeIssue, string, error) {
	if milestone > 0 {
		list, err := client.ListIssuesByMilestone(ctx, milestone)
		if err != nil {
			return nil, "", fmt.Errorf("list issues for milestone %d: %w", milestone, err)
		}
		members := make([]scopeIssue, 0, len(list))
		for _, iss := range list {
			members = append(members, scopeIssue{number: int(iss.Index), title: iss.Title})
		}
		return members, fmt.Sprintf("milestone:%d", milestone), nil
	}
	members := make([]scopeIssue, 0, len(issues))
	for _, n := range issues {
		iss, err := client.GetIssue(ctx, n)
		if err != nil {
			return nil, "", fmt.Errorf("fetch issue #%d: %w", n, err)
		}
		members = append(members, scopeIssue{number: int(iss.Index), title: iss.Title})
	}
	return members, "issues:" + joinIssueNumbers(issues), nil
}

// seedScopeGeneration writes one scope_generations row, its scope_membership, and
// one pending work_item per member, all in a single transaction.
func seedScopeGeneration(ctx context.Context, db *sql.DB, repo string, milestone int64, sourceQuery string, members []scopeIssue) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var milestoneArg any
	if milestone > 0 {
		milestoneArg = milestone
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO scope_generations (repo, milestone_id, scope_hash, source_query) VALUES (?, ?, ?, ?)`,
		repo, milestoneArg, hashScope(members), sourceQuery)
	if err != nil {
		return 0, fmt.Errorf("insert scope generation: %w", err)
	}
	genID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, m := range members {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO scope_membership (scope_generation_id, artifact_kind, artifact_number, disposition) VALUES (?, 'issue', ?, 'in_scope')`,
			genID, m.number); err != nil {
			return 0, fmt.Errorf("insert scope membership #%d: %w", m.number, err)
		}
		workItemID := uuid.New().String()
		ins, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO work_items (id, kind, source_id, title, scope_generation, state) VALUES (?, 'resolve_issue', ?, ?, ?, 'pending')`,
			workItemID, strconv.Itoa(m.number), m.title, genID)
		if err != nil {
			return 0, fmt.Errorf("insert work item #%d: %w", m.number, err)
		}
		if n, _ := ins.RowsAffected(); n > 0 {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO work_item_state_transitions (work_item_id, from_state, to_state, reason) VALUES (?, NULL, 'pending', 'seeded by sprint start')`,
				workItemID); err != nil {
				return 0, fmt.Errorf("insert work item transition #%d: %w", m.number, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return genID, nil
}

// commitSessionSnapshot dumps session.db to session.sql and commits the session
// repo, so `sprint run` opens from a clean tree (and doesn't mistake the seed for
// crashed-run state).
func commitSessionSnapshot(ctx context.Context, dir, message string) error {
	runner := store.ExecRunner{}
	if err := store.DumpToFile(filepath.Join(dir, "session.db"), filepath.Join(dir, "session.sql")); err != nil {
		return fmt.Errorf("dump session: %w", err)
	}
	if out, err := runner.Run(ctx, dir, "git", "add", ".gitignore", "session.sql", "artifacts"); err != nil {
		return fmt.Errorf("stage session snapshot: %w: %s", err, string(out))
	}
	if out, err := runner.Run(ctx, dir, "git", "commit", "-m", message); err != nil {
		return fmt.Errorf("commit session snapshot: %w: %s", err, string(out))
	}
	return nil
}

// hashScope is a stable short hash of the sorted member set, recorded on the
// scope generation for drift detection.
func hashScope(members []scopeIssue) string {
	nums := make([]int, len(members))
	for i, m := range members {
		nums[i] = m.number
	}
	sort.Ints(nums)
	h := sha256.New()
	for _, n := range nums {
		fmt.Fprintf(h, "%d,", n)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func joinIssueNumbers(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
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

			// Session dir is created by `sprint start`; `run` requires it to exist.
			sessionDir, err := sprintSessionDir(id)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(sessionDir, "session.db")); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("sprint run: session %q not found; run `botfam sprint start %s` first", id, id)
				}
				return fmt.Errorf("stat session db: %w", err)
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

			// Open session repo (created by `sprint start`)
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

			fctx, err := famctx.ResolveAgentRuntime(wd)
			if err != nil {
				return fmt.Errorf("resolve agent runtime context: %w", err)
			}

			executor := mcp.NewForgeExecutor(fctx)
			wcHandler := workerchannel.Service{
				DB:       db,
				Executor: executor,
			}
			wcPath, wcMux := wcHandler.Handler()
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
