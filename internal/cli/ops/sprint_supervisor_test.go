package ops_test

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/cli/ops"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
)

// HelperProcess is a command-line entrypoint for spawning tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	// If requested to sleep, sleep to test TTL reaping
	if os.Getenv("HELPER_SLEEP") == "1" {
		time.Sleep(5 * time.Second)
		return
	}

	// Just print env and exit
	fmt.Printf("TRACEPARENT=%s\n", os.Getenv("TRACEPARENT"))
	fmt.Printf("BOTFAM_WORKER_ID=%s\n", os.Getenv("BOTFAM_WORKER_ID"))
	fmt.Printf("BOTFAM_WORK_ITEM_ID=%s\n", os.Getenv("BOTFAM_WORK_ITEM_ID"))
	fmt.Printf("BOTFAM_WORKER_CHANNEL_SOCKET=%s\n", os.Getenv("BOTFAM_WORKER_CHANNEL_SOCKET"))
	fmt.Printf("BOTFAM_FENCING_TOKEN=%s\n", os.Getenv("BOTFAM_FENCING_TOKEN"))
}

func TestSprintSupervisorLifecycle(t *testing.T) {
	// Setup user home directory redirect to a temp dir
	tmpHome, err := ioutil.TempDir("", "botfam-home-supervisor-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Create a mock git repository to satisfy ResolveRepoName
	tempDir, err := os.MkdirTemp("", "botfam-sprint-supervisor-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	gitDir := filepath.Join(tempDir, "botfam")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	sessionID := "super"
	sessionDir := filepath.Join(tmpHome, ".botfam", "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, sessionDir, "init")
	runGit(t, sessionDir, "config", "user.name", "test")
	runGit(t, sessionDir, "config", "user.email", "test@example.invalid")


	// 1. Initialize the database and pre-insert a pending work item
	dbPath := filepath.Join(sessionDir, "session.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}

	// Insert run-1 as already completed
	_, err = db.Exec(`INSERT INTO runs (id, session_id, status) VALUES ('run-1', ?, 'completed')`, sessionID)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}

	// Insert a pending work item
	_, err = db.Exec(`INSERT INTO work_items (id, kind, source_id, title, scope_generation, state) VALUES ('work-item-1', 'reply_to_issue', 'issue-1', 'Test Issue', 1, 'pending')`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	// 2. Start the supervisor using the test binary self-execution as the worker command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := ops.NewSprintCmd()
	cmd.SetContext(ctx)

	workerCmd := fmt.Sprintf("%s -test.run=TestHelperProcess", os.Args[0])
	os.Setenv("GO_WANT_HELPER_PROCESS", "1")
	defer os.Unsetenv("GO_WANT_HELPER_PROCESS")

	var out bytes.Buffer
	var runErr error
	runFinished := make(chan struct{})
	go func() {
		runErr = cmdutil.RunCobra(cmd, []string{"run", sessionID, "--worker-command", workerCmd, "--worker-ttl", "1s"}, &out)
		close(runFinished)
	}()

	// Wait for supervisor to start and process the work item
	time.Sleep(2 * time.Second)
	cancel() // Shut down supervisor
	<-runFinished

	if runErr != nil {
		errStr := runErr.Error()
		if strings.Contains(errStr, "operation not permitted") || strings.Contains(errStr, "permission denied") {
			t.Skipf("sandbox does not permit unix socket bind: %v", runErr)
		}
		t.Fatalf("Supervisor exited with error: %v. Output: %s", runErr, out.String())
	}
	output := out.String()
	t.Logf("Supervisor Output: %s", output)

	if !strings.Contains(output, "BOTFAM_WORKER_CHANNEL_SOCKET=") || strings.Contains(output, "BOTFAM_WORKER_CHANNEL_SOCKET=\n") {
		t.Error("expected worker env to contain non-empty BOTFAM_WORKER_CHANNEL_SOCKET")
	}
	if !strings.Contains(output, "BOTFAM_FENCING_TOKEN=") || strings.Contains(output, "BOTFAM_FENCING_TOKEN=\n") {
		t.Error("expected worker env to contain non-empty BOTFAM_FENCING_TOKEN")
	}

	// Verify that run-2 was created and incremented, and the work item was completed/failed
	db, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var runsCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runsCount)
	if runsCount < 2 {
		t.Errorf("expected at least 2 runs, got %d", runsCount)
	}

	var newRunStatus string
	_ = db.QueryRow(`SELECT status FROM runs WHERE id = 'run-2'`).Scan(&newRunStatus)
	if newRunStatus == "" {
		t.Error("expected run-2 to be inserted")
	}

	var workItemState string
	_ = db.QueryRow(`SELECT state FROM work_items WHERE id = 'work-item-1'`).Scan(&workItemState)
	if workItemState == "pending" {
		t.Error("expected work-item-1 to have been processed, but remained pending")
	}
}

func TestSprintSupervisorTTLReaping(t *testing.T) {
	// Setup user home directory redirect to a temp dir
	tmpHome, err := ioutil.TempDir("", "botfam-home-reaping-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Create a mock git repository
	tempDir, err := os.MkdirTemp("", "botfam-sprint-reaping-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	gitDir := filepath.Join(tempDir, "botfam")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()
	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	sessionID := "reap"
	sessionDir := filepath.Join(tmpHome, ".botfam", "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, sessionDir, "init")
	runGit(t, sessionDir, "config", "user.name", "test")
	runGit(t, sessionDir, "config", "user.email", "test@example.invalid")

	dbPath := filepath.Join(sessionDir, "session.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	// Insert a pending work item
	_, err = db.Exec(`INSERT INTO work_items (id, kind, source_id, title, scope_generation, state) VALUES ('work-item-timeout', 'reply_to_issue', 'issue-2', 'Timeout Issue', 1, 'pending')`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := ops.NewSprintCmd()
	cmd.SetContext(ctx)

	workerCmd := fmt.Sprintf("%s -test.run=TestHelperProcess", os.Args[0])
	os.Setenv("GO_WANT_HELPER_PROCESS", "1")
	os.Setenv("HELPER_SLEEP", "1")
	defer os.Unsetenv("GO_WANT_HELPER_PROCESS")
	defer os.Unsetenv("HELPER_SLEEP")

	var out bytes.Buffer
	var runErr error
	runFinished := make(chan struct{})
	go func() {
		runErr = cmdutil.RunCobra(cmd, []string{"run", sessionID, "--worker-command", workerCmd, "--worker-ttl", "1s"}, &out)
		close(runFinished)
	}()

	// Wait for TTL reaping to trigger
	time.Sleep(3 * time.Second)
	cancel()
	<-runFinished

	if runErr != nil {
		errStr := runErr.Error()
		if strings.Contains(errStr, "operation not permitted") || strings.Contains(errStr, "permission denied") {
			t.Skipf("sandbox does not permit unix socket bind: %v", runErr)
		}
		t.Fatalf("Supervisor exited with error: %v. Output: %s", runErr, out.String())
	}
	t.Logf("Supervisor Output: %s", out.String())

	db, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var workItemState string
	_ = db.QueryRow(`SELECT state FROM work_items WHERE id = 'work-item-timeout'`).Scan(&workItemState)
	if workItemState != "failed" {
		t.Errorf("expected work item to be reaped and marked failed, got %q", workItemState)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

