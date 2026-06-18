package review

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/gitexec"
	"github.com/spf13/cobra"
)

// NewVerifyCmd builds the `botfam verify` Cobra command.
//
// It creates a DETACHED ephemeral git worktree at <sha> under a temp dir,
// runs `go build ./...` and `go test <pkgs or ./...>` inside it, then always
// removes the worktree (even on failure), and reports pass/fail plus a short
// summary. This automates the manual
//
//	git worktree add --detach /tmp/x <sha>; (cd /tmp/x && go build/test); git worktree remove
//
// dance used before ccrep approvals.
func NewVerifyCmd() *cobra.Command {
	var race bool
	c := &cobra.Command{
		Use:   "verify <sha> [pkgs...]",
		Short: "Build and test a commit in an ephemeral worktree",
		Long: `Create a detached ephemeral git worktree at <sha>, run "go build ./..."
and "go test <pkgs or ./...>" inside it, then remove the worktree and report
pass/fail. The worktree is always cleaned up, even on failure.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVerify(args[0], args[1:], race, cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVar(&race, "race", false, "Run tests with data race detector")
	return c
}

func runVerify(sha string, pkgs []string, race bool, out io.Writer) error {
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	repo := famconfig.RepoPath(".")

	// Resolve the sha to a concrete commit so the report and the worktree
	// reference the same revision, and so we fail early on a bad ref.
	resolved, err := gitexec.One(repo, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return fmt.Errorf("cannot resolve %q to a commit: %w", sha, err)
	}

	scratchTmp := filepath.Join(repo, "scratch", "tmp")
	if err := os.MkdirAll(scratchTmp, 0o755); err != nil {
		return fmt.Errorf("failed to create scratch tmp dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(scratchTmp, "botfam-verify-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	wtPath := filepath.Join(tmpDir, "wt")

	// Create a detached worktree at the resolved commit.
	if _, err := gitexec.Output(repo, "worktree", "add", "--detach", wtPath, resolved); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to add ephemeral worktree: %w", err)
	}

	// Always clean up the worktree and temp dir, even on build/test failure.
	defer func() {
		// `worktree remove --force` detaches the linked worktree from git's
		// metadata; RemoveAll then clears the temp dir itself.
		_, _ = gitexec.Output(repo, "worktree", "remove", "--force", wtPath)
		_ = os.RemoveAll(tmpDir)
	}()

	fmt.Fprintf(out, "Verifying %s in ephemeral worktree %s\n", short(resolved), wtPath)

	// go build ./...
	fmt.Fprintln(out, "==> go build ./...")
	buildOut, buildErr := runGo(wtPath, "build", "./...")
	writeGoOutput(out, buildOut)
	if buildErr != nil {
		fmt.Fprintf(out, "RESULT: FAIL — build failed for %s\n", short(resolved))
		return fmt.Errorf("go build failed: %w", buildErr)
	}

	// go test <pkgs...>
	testArgs := []string{"test"}
	if race {
		testArgs = append(testArgs, "-race")
	}
	testArgs = append(testArgs, pkgs...)
	fmt.Fprintf(out, "==> go %s\n", strings.Join(testArgs, " "))
	testOut, testErr := runGo(wtPath, testArgs...)
	writeGoOutput(out, testOut)
	if testErr != nil {
		fmt.Fprintf(out, "RESULT: FAIL — tests failed for %s\n", short(resolved))
		return fmt.Errorf("go test failed: %w", testErr)
	}

	fmt.Fprintf(out, "RESULT: PASS — build + test (%s) clean at %s\n", strings.Join(pkgs, " "), short(resolved))
	return nil
}

// runGo runs `go <args...>` in dir, returning combined stdout+stderr and any
// error. Output is captured (not streamed) so the caller controls reporting.
func runGo(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func writeGoOutput(out io.Writer, s string) {
	if s == "" {
		return
	}
	fmt.Fprint(out, s)
	if !strings.HasSuffix(s, "\n") {
		fmt.Fprintln(out)
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
