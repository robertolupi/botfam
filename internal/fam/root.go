package fam

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rlupi/botfam/internal/store"
)

type Resolver struct {
	WorkDir string
	Env     []string
}

type RootInfo struct {
	Root      string
	Name      string
	RootSet   []string
	RootSetID string
	Explicit  bool
	Actor     string
}

func (r Resolver) Resolve() (RootInfo, error) {
	var parsedActor string
	if absDir, err := filepath.Abs(r.WorkDir); err == nil {
		parsedActor = parseActor(filepath.Base(absDir))
	}

	if envActor := getenv(r.Env, "COLLAB_ACTOR"); parsedActor != "" && envActor != "" && envActor != parsedActor {
		return RootInfo{}, fmt.Errorf("COLLAB_ACTOR %q conflicts with resolved directory actor %q", envActor, parsedActor)
	}

	if root := getenv(r.Env, "COLLAB_ROOT"); root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return RootInfo{}, err
		}
		return RootInfo{
			Root:     abs,
			Name:     filepath.Base(abs),
			Explicit: true,
			Actor:    parsedActor,
		}, nil
	}

	roots, err := gitLines(r.WorkDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return RootInfo{}, makeNoGitHistoryError()
	}
	sort.Strings(roots)
	sum := sha256.Sum256([]byte(strings.Join(roots, "\n")))
	id := hex.EncodeToString(sum[:])[:12]
	name := "fam-" + id
	if suffix := getenv(r.Env, "BOTFAM_FAM"); suffix != "" {
		name += "-" + sanitizeSuffix(suffix)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return RootInfo{}, err
	}
	return RootInfo{
		Root:      filepath.Join(home, ".botfam", name),
		Name:      name,
		RootSet:   roots,
		RootSetID: id,
		Actor:     parsedActor,
	}, nil
}

// parseActor derives an actor name from a worktree directory basename per
// doc/collab/PROTOCOL.md §1: the actor is the basename with a leading "wt-"
// or "botfam-" prefix stripped. Basenames without one of those prefixes yield
// no actor (fail closed); callers fall back to their existing "no actor" paths.
func parseActor(base string) string {
	var actor string
	switch {
	case strings.HasPrefix(base, "wt-"):
		actor = strings.TrimPrefix(base, "wt-")
	case strings.HasPrefix(base, "botfam-"):
		actor = strings.TrimPrefix(base, "botfam-")
	default:
		return ""
	}
	if actor == "" || store.ValidateName("actor", actor) != nil {
		return ""
	}
	return actor
}

func GitObjectStores(workDir string) ([]string, error) {
	common, err := gitOne(workDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(workDir, common)
	}
	objects := filepath.Join(common, "objects")
	out := []string{}
	// Canonicalize to an absolute, symlink-resolved path so membership is matched
	// on real Git object identity, not on a path string. git rev-parse can return
	// a relative ".git" from a repo root, and EvalSymlinks of a relative path stays
	// relative — which would collapse every repo's store to ".git/objects" and match
	// any fam. Absolutize first, then resolve symlinks.
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if rp, err := filepath.EvalSymlinks(abs); err == nil {
			out = append(out, rp)
		} else {
			out = append(out, abs)
		}
	}
	add(objects)
	alts := filepath.Join(objects, "info", "alternates")
	if b, err := os.ReadFile(alts); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !filepath.IsAbs(line) {
				line = filepath.Join(objects, line)
			}
			add(line)
		}
	}
	return unique(out), nil
}

func RepoPath(workDir string) string {
	if top, err := gitOne(workDir, "rev-parse", "--show-toplevel"); err == nil {
		if rp, err := filepath.EvalSymlinks(top); err == nil {
			return rp
		}
		return top
	}
	abs, _ := filepath.Abs(workDir)
	return abs
}

func gitLines(workDir string, args ...string) ([]string, error) {
	out, err := gitOutput(workDir, args...)
	if err != nil {
		return nil, err
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func gitOne(workDir string, args ...string) (string, error) {
	lines, err := gitLines(workDir, args...)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("git %s returned no output", strings.Join(args, " "))
	}
	return lines[0], nil
}

func gitOutput(workDir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func getenv(env []string, key string) string {
	for _, item := range env {
		if k, v, ok := strings.Cut(item, "="); ok && k == key {
			return v
		}
	}
	return os.Getenv(key)
}

func sanitizeSuffix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func makeNoGitHistoryError() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("COLLAB_ROOT is unset and no git history could be used to derive a fam root; run botfam setup or set COLLAB_ROOT")
	}
	botfamDir := filepath.Join(home, ".botfam")
	entries, err := os.ReadDir(botfamDir)
	if err != nil {
		return errors.New("COLLAB_ROOT is unset and no git history could be used to derive a fam root; run botfam setup or set COLLAB_ROOT")
	}

	var sb strings.Builder
	sb.WriteString("COLLAB_ROOT is unset and no git history could be used to derive a fam root.\n")
	sb.WriteString("To fix this, either run from a member worktree, or set COLLAB_ROOT explicitly:\n")
	sb.WriteString("  COLLAB_ROOT=~/.botfam/<fam> botfam <command>\n\n")

	var fams []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tomlPath := filepath.Join(botfamDir, entry.Name(), "fam.toml")
		if _, err := os.Stat(tomlPath); err == nil {
			reg, err := ReadRegistry(tomlPath)
			if err == nil {
				fams = append(fams, fmt.Sprintf("  - %s (at ~/.botfam/%s)\n    Member repos:\n      * %s",
					reg.Name, entry.Name(), strings.Join(reg.RepoPaths, "\n      * ")))
			}
		}
	}

	if len(fams) > 0 {
		sb.WriteString("Available families under ~/.botfam:\n")
		sb.WriteString(strings.Join(fams, "\n") + "\n")
	} else {
		sb.WriteString("No configured families found under ~/.botfam. Run 'botfam setup' to initialize one.\n")
	}
	return errors.New(strings.TrimSuffix(sb.String(), "\n"))
}
