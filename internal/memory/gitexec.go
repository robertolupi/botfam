package memory

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WikiGit is the production gitOps backend: a local working clone of a
// <repo>.wiki.git repository. The wiki is the source of truth for shared facts
// (proposal §4.1); this type owns the git plumbing behind the store's CAS
// contract. It is not safe for concurrent use within a process — one clone, one
// writer at a time; cross-process/cross-agent concurrency is handled by the
// store's push/rebase loop.
type WikiGit struct {
	dir    string // working clone
	branch string // wiki default branch (gitea: "master")
}

// gitEmail maps an actor to its per-worktree commit identity, matching the
// botfam convention (roberto.lupi+<actor>@gmail.com).
func gitEmail(actor string) string {
	if actor == "" {
		actor = "botfam"
	}
	return "roberto.lupi+" + actor + "@gmail.com"
}

// CloneWiki clones authURL (a token-authenticated <repo>.wiki.git URL) into dir.
// If dir already contains a clone it is reused after a fetch. When branch is
// empty the remote's default branch is detected (Gitea wikis vary between
// "master" and "main"), falling back to "master".
func CloneWiki(authURL, dir, branch string) (*WikiGit, error) {
	if branch == "" {
		branch = detectDefaultBranch(authURL)
	}
	w := &WikiGit{dir: dir, branch: branch}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		if _, err := w.run("remote", "set-url", "origin", authURL); err != nil {
			return nil, err
		}
		if _, err := w.run("fetch", "origin"); err != nil {
			return nil, err
		}
		if _, err := w.run("reset", "--hard", "origin/"+branch); err != nil {
			return nil, err
		}
		return w, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if out, err := runGit("", "clone", "--branch", branch, authURL, dir); err != nil {
		return nil, fmt.Errorf("clone wiki: %v: %s", err, out)
	}
	return w, nil
}

func (w *WikiGit) ReadFile(rel string) ([]byte, bool, error) {
	b, err := os.ReadFile(filepath.Join(w.dir, rel))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (w *WikiGit) WriteCommit(rel, content, author, message string) error {
	path := filepath.Join(w.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	if _, err := w.run("add", rel); err != nil {
		return err
	}
	ident := fmt.Sprintf("%s <%s>", author, gitEmail(author))
	if out, err := w.run(
		"-c", "user.name="+author,
		"-c", "user.email="+gitEmail(author),
		"commit", "-m", message, "--author", ident,
	); err != nil {
		return fmt.Errorf("commit: %v: %s", err, out)
	}
	return nil
}

func (w *WikiGit) Push() (bool, error) {
	out, err := w.run("push", "origin", "HEAD:"+w.branch)
	if err == nil {
		return false, nil
	}
	// A non-fast-forward rejection is the CAS signal, not a transport failure.
	if isNonFastForward(out) {
		return true, nil
	}
	return false, fmt.Errorf("push: %v: %s", err, out)
}

func (w *WikiGit) PullRebase() error {
	out, err := w.run("pull", "--rebase", "origin", w.branch)
	if err == nil {
		return nil
	}
	// Leave the tree clean for the next attempt / caller.
	_, _ = w.run("rebase", "--abort")
	return fmt.Errorf("rebase: %v: %s", err, strings.TrimSpace(out))
}

func (w *WikiGit) run(args ...string) (string, error) { return runGit(w.dir, args...) }

// detectDefaultBranch asks the remote for its HEAD symref without a local clone,
// so we clone the right branch on the first try. Falls back to "master".
func detectDefaultBranch(authURL string) string {
	out, err := runGit("", "ls-remote", "--symref", authURL, "HEAD")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ref:") && strings.Contains(line, "HEAD") {
				// "ref: refs/heads/main\tHEAD"
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					return strings.TrimPrefix(fields[1], "refs/heads/")
				}
			}
		}
	}
	return "master"
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func isNonFastForward(gitOutput string) bool {
	s := strings.ToLower(gitOutput)
	return strings.Contains(s, "non-fast-forward") ||
		strings.Contains(s, "fetch first") ||
		strings.Contains(s, "rejected")
}

// WikiAuthURL builds a token-authenticated clone URL for owner/repo's wiki from
// a Gitea baseURL (e.g. "http://gitea:3000/"). The token is sent as the
// username, which Gitea accepts for git-over-HTTP.
func WikiAuthURL(baseURL, owner, repo, token string) string {
	base := strings.TrimSuffix(baseURL, "/")
	scheme, rest, ok := strings.Cut(base, "://")
	path := fmt.Sprintf("%s/%s/%s.wiki.git", base, owner, repo)
	if ok && token != "" {
		path = fmt.Sprintf("%s://%s@%s/%s/%s.wiki.git", scheme, token, rest, owner, repo)
	}
	return path
}
