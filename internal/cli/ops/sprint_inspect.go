package ops

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"github.com/spf13/cobra"
)

// repoLiveness reports whether a live supervisor currently holds the lease for a
// repo. Abstracted so tests can inject liveness without a real session file.
type repoLiveness func(ctx context.Context, repo string) bool

func defaultRepoLiveness(ctx context.Context, repo string) bool {
	if repo == "" {
		return false
	}
	resp, err := singlehost.NewSessionResolver().Resolve(ctx, connect.NewRequest(&pb.Scope{RepoName: repo}))
	return err == nil && resp.Msg.GetFound()
}

// sprintSessionsRoot returns ~/.botfam/sessions.
func sprintSessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".botfam", "sessions"), nil
}

func newSprintShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show $ID",
		Short: "Show a sprint session's runs, work items, dispatches, and artifacts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			dir, err := sprintSessionDir(id)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, "session.db")); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("sprint show: session %q not found", id)
				}
				return err
			}
			return runSprintShow(cmd.Context(), cmd.OutOrStdout(), dir, id)
		},
	}
}

// sessionSummary is one row of `sprint ls`.
type sessionSummary struct {
	id         string
	state      string
	runs       int
	lastRun    string
	lastStatus string
	repo       string
	scope      string
	counts     map[string]int // work_item state -> count
}

// deriveSessionState maps the latest run status + repo lease liveness (+ an
// explicit operator `ended` marker) onto a session state. NB liveness is
// repo-level (the single-host lease is per repo), so the running/crashed split
// assumes v0's one-live-session-per-repo model.
func deriveSessionState(lastRunStatus string, repoLive, ended bool) string {
	if ended {
		return "ended" // operator-closed via `sprint end`
	}
	switch lastRunStatus {
	case "":
		return "new" // started, never run
	case "running":
		if repoLive {
			return "running"
		}
		return "crashed"
	default:
		return lastRunStatus // completed, etc.
	}
}

func loadSessionSummary(ctx context.Context, sessionDir, id string, live repoLiveness) (sessionSummary, error) {
	s := sessionSummary{id: id, counts: map[string]int{}}
	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		return s, err
	}
	defer db.Close()

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&s.runs)
	_ = db.QueryRowContext(ctx, `SELECT id, status FROM runs ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&s.lastRun, &s.lastStatus)
	_ = db.QueryRowContext(ctx, `SELECT repo, source_query FROM scope_generations ORDER BY id DESC LIMIT 1`).Scan(&s.repo, &s.scope)

	rows, err := db.QueryContext(ctx, `SELECT state, COUNT(*) FROM work_items GROUP BY state`)
	if err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				s.counts[st] = n
			}
		}
		rows.Close()
	}

	// session_meta may be absent on pre-v4 session dbs; ignore the error.
	var endedAt string
	_ = db.QueryRowContext(ctx, `SELECT value FROM session_meta WHERE key = 'ended_at'`).Scan(&endedAt)

	s.state = deriveSessionState(s.lastStatus, live(ctx, s.repo), endedAt != "")
	return s, nil
}

// ownLiveSupervisor reports whether the repo's live supervisor is running this
// session — the only case in which `sprint end <id>` may signal it. The lease is
// repo-level, so a live supervisor for the same repo but a *different* (or
// unproven/empty) session id must never be signaled. targetID is the session
// being ended; liveID is the stamped session id from the live session file.
func ownLiveSupervisor(targetID, liveID string, live bool) bool {
	return live && liveID != "" && liveID == targetID
}

// runSprintEnd stops a live supervisor for the session (if any) and records a
// durable `ended` marker so ls/show reflect it. This is also the seam where
// post-session analytics will eventually run (#531).
func runSprintEnd(ctx context.Context, w io.Writer, sessionDir, id string, timeout time.Duration) error {
	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	// Ensure session_meta exists even on an older session db.
	if err := store.ApplyMigrations(ctx, db); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	var repo string
	_ = db.QueryRowContext(ctx, `SELECT repo FROM scope_generations ORDER BY id DESC LIMIT 1`).Scan(&repo)

	if repo != "" {
		pid, liveID, ok := singlehost.LiveSupervisorPID(repo)
		switch {
		case ownLiveSupervisor(id, liveID, ok):
			// The live supervisor for this repo is running *this* session — stop it.
			fmt.Fprintf(w, "Stopping live supervisor (pid %d)…\n", pid)
			if proc, perr := os.FindProcess(pid); perr == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
			deadline := time.Now().Add(timeout)
			for {
				_, gone, live := singlehost.LiveSupervisorPID(repo)
				if !ownLiveSupervisor(id, gone, live) {
					break // our target supervisor has exited (or the lease moved on)
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("sprint end: supervisor (pid %d) did not stop within %s", pid, timeout)
				}
				time.Sleep(200 * time.Millisecond)
			}
		case ok:
			// A live supervisor holds this repo's lease but for a *different*
			// session (or an unproven one). Never signal it — only mark our own
			// session ended. (Repo-level lease; the live session is identified by
			// the stamped session id.)
			fmt.Fprintf(w, "note: repo %q has a live supervisor for a different session (%q); not signaling it\n", repo, liveID)
		}
	}

	if _, err := db.ExecContext(ctx, `INSERT OR REPLACE INTO session_meta (key, value) VALUES ('ended_at', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`); err != nil {
		return fmt.Errorf("mark session ended: %w", err)
	}
	if err := commitSessionSnapshot(ctx, sessionDir, fmt.Sprintf("sprint end %s", id)); err != nil {
		return err
	}
	fmt.Fprintf(w, "Sprint %s ended.\n", id)
	return nil
}

// runSprintLs lists every session under root, read-only (WAL-safe: SELECT only,
// never locks a live supervisor's db).
func runSprintLs(ctx context.Context, w io.Writer, root string, live repoLiveness) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "No sprint sessions.")
			return nil
		}
		return fmt.Errorf("read sessions dir: %w", err)
	}

	var summaries []sessionSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "session.db")); err != nil {
			continue
		}
		if s, err := loadSessionSummary(ctx, dir, e.Name(), live); err == nil {
			summaries = append(summaries, s)
		}
	}
	if len(summaries) == 0 {
		fmt.Fprintln(w, "No sprint sessions.")
		return nil
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].id < summaries[j].id })

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tSTATE\tRUNS\tLAST RUN\tPENDING\tRUNNING\tDONE\tFAILED\tSCOPE")
	for _, s := range summaries {
		lastRun := "-"
		if s.lastRun != "" {
			lastRun = fmt.Sprintf("%s (%s)", s.lastRun, s.lastStatus)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\t%d\t%d\t%d\t%s\n",
			s.id, s.state, s.runs, lastRun,
			s.counts["pending"], s.counts["running"], s.counts["completed"], s.counts["failed"], s.scope)
	}
	return tw.Flush()
}

// runSprintShow prints the read model of one session: runs, work items,
// dispatches, and artifacts.
func runSprintShow(ctx context.Context, w io.Writer, sessionDir, id string) error {
	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	var repo, scope string
	_ = db.QueryRowContext(ctx, `SELECT repo, source_query FROM scope_generations ORDER BY id DESC LIMIT 1`).Scan(&repo, &scope)
	fmt.Fprintf(w, "Session: %s\nDir:     %s\nRepo:    %s\nScope:   %s\n", id, sessionDir, repo, scope)
	var endedAt string
	_ = db.QueryRowContext(ctx, `SELECT value FROM session_meta WHERE key = 'ended_at'`).Scan(&endedAt)
	if endedAt != "" {
		fmt.Fprintf(w, "Ended:   %s\n", endedAt)
	}

	renderQuery(ctx, w, db, "Runs",
		`SELECT id, status, started_at, COALESCE(completed_at,'-') FROM runs ORDER BY started_at, id`,
		"ID\tSTATUS\tSTARTED\tCOMPLETED")
	renderQuery(ctx, w, db, "Work items",
		`SELECT substr(id,1,8), kind, source_id, state, substr(title,1,60) FROM work_items ORDER BY created_at, id`,
		"ID\tKIND\tSOURCE\tSTATE\tTITLE")
	renderQuery(ctx, w, db, "Dispatches",
		`SELECT substr(work_item_id,1,8), worker_id, dispatched_at, COALESCE(completed_at,'-') FROM dispatches ORDER BY dispatched_at, id`,
		"WORK ITEM\tWORKER\tDISPATCHED\tCOMPLETED")
	renderQuery(ctx, w, db, "Artifacts",
		`SELECT kind, uri, substr(work_item_id,1,8) FROM artifacts ORDER BY created_at, id`,
		"KIND\tURI\tWORK ITEM")
	return nil
}

// renderQuery prints a titled, tab-aligned table for a SELECT whose columns are
// display-ready. A NULL cell renders as "-"; an empty result prints "(none)".
func renderQuery(ctx context.Context, w io.Writer, db *sql.DB, title, query, header string) {
	fmt.Fprintf(w, "\n%s:\n", title)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		fmt.Fprintf(w, "  (error: %v)\n", err)
		return
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		fmt.Fprintf(w, "  (error: %v)\n", err)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "  "+header)

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		cells := make([]string, len(cols))
		for i := range vals {
			cells[i] = cellString(vals[i])
		}
		fmt.Fprintln(tw, "  "+strings.Join(cells, "\t"))
		n++
	}
	tw.Flush()
	if n == 0 {
		fmt.Fprintln(w, "  (none)")
	}
}

// cellString renders a scanned SQL value for display.
func cellString(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case []byte:
		return string(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}
