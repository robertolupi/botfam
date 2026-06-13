package fam

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VerifyCmd handles "botfam verify <sha> [pkgs...]".
//
// It creates a DETACHED ephemeral git worktree at <sha> under a temp dir,
// runs `go build ./...` and `go test <pkgs or ./...>` inside it, then always
// removes the worktree (even on failure), and reports pass/fail plus a short
// summary. This automates the manual
//
//	git worktree add --detach /tmp/x <sha>; (cd /tmp/x && go build/test); git worktree remove
//
// dance used before ccrep approvals.
func VerifyCmd(args []string, out io.Writer) error {
	var sha string
	var pkgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return printVerifyHelp(out)
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("unknown argument %q", arg)
		case sha == "":
			sha = arg
		default:
			pkgs = append(pkgs, arg)
		}
	}

	if sha == "" {
		return fmt.Errorf("usage: botfam verify <sha> [pkgs...]")
	}
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	repo := RepoPath(".")

	// Resolve the sha to a concrete commit so the report and the worktree
	// reference the same revision, and so we fail early on a bad ref.
	resolved, err := gitOne(repo, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return fmt.Errorf("cannot resolve %q to a commit: %w", sha, err)
	}

	tmpDir, err := os.MkdirTemp("", "botfam-verify-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	wtPath := filepath.Join(tmpDir, "wt")

	// Create a detached worktree at the resolved commit.
	if _, err := gitOutput(repo, "worktree", "add", "--detach", wtPath, resolved); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to add ephemeral worktree: %w", err)
	}

	// Always clean up the worktree and temp dir, even on build/test failure.
	defer func() {
		// `worktree remove --force` detaches the linked worktree from git's
		// metadata; RemoveAll then clears the temp dir itself.
		_, _ = gitOutput(repo, "worktree", "remove", "--force", wtPath)
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
	testArgs := append([]string{"test"}, pkgs...)
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

func printVerifyHelp(out io.Writer) error {
	fmt.Fprint(out, "Usage:\n  botfam verify <sha> [pkgs...]\n\n"+
		"Create a detached ephemeral git worktree at <sha>, run `go build ./...`\n"+
		"and `go test <pkgs or ./...>` inside it, then remove the worktree and\n"+
		"report pass/fail. Always cleans up the worktree, even on failure.\n")
	return nil
}
