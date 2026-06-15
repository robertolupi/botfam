package famconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver derives a fam Root/Name/Actor for a worktree. It is the dependency-free
// home of identity resolution (#311): both internal/cli and internal/mcp resolve
// through it without importing each other. internal/fam re-exports Resolver and
// RootInfo via type aliases so existing callers are unaffected.
type Resolver struct {
	WorkDir string
	Env     []string
}

// RootInfo is the resolved fam root for a worktree.
type RootInfo struct {
	Root      string
	Name      string
	RootSet   []string
	RootSetID string
	Explicit  bool
	Actor     string
}

func (r Resolver) Resolve() (RootInfo, error) {
	repoName := ResolveRepoName(r.WorkDir)
	var parsedActor string
	var unifiedRoot string
	var unifiedName string

	if absDir, err := filepath.Abs(r.WorkDir); err == nil {
		if evalDir, err := filepath.EvalSymlinks(absDir); err == nil {
			absDir = evalDir
		}
		gitRoot, _ := gitOne(absDir, "rev-parse", "--show-toplevel")
		if evalRoot, err := filepath.EvalSymlinks(gitRoot); err == nil {
			gitRoot = evalRoot
		}
		curr := absDir
		for {
			actor := ParseActor(filepath.Base(curr), repoName)
			if actor != "" {
				parsedActor = actor
				break
			}
			if curr == gitRoot || curr == filepath.Dir(curr) {
				break
			}
			curr = filepath.Dir(curr)
		}
		// Bare-name worktrees: the wt- prefix is retired (agent name =
		// basename). When the prefix-based ParseActor finds nothing, accept the
		// worktree-root basename if it is a declared [agent.<name>]/[user.<name>]
		// in the canonical fam.toml. Locate+read it through the shared famconfig
		// primitives — the one fam.toml finder every consumer uses (#252) —
		// rather than re-deriving <fam-dir>/fam.toml here. ResolveFam isn't used
		// directly: it fail-closes on [user.<name>]/base checkouts, which Resolve
		// must still derive a Root/Name for. (FindFamTOMLPath, not
		// LoadFamRegistry, so we don't recurse back through Resolve.)
		if gitRoot != "" {
			if famTOMLPath := FindFamTOMLPath(r.WorkDir, r.Env); famTOMLPath != "" {
				if reg, err := ReadRegistry(famTOMLPath); err == nil {
					famDir := filepath.Dir(famTOMLPath)
					base := filepath.Base(gitRoot)
					if _, ok := reg.Agents[base]; ok {
						parsedActor = base
					} else if _, ok := reg.Users[base]; ok {
						parsedActor = base
					}
					unifiedRoot = famDir
					unifiedName = reg.Name
					if unifiedName == "" {
						unifiedName = filepath.Base(famDir)
					}
				}
			}
		}
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

	if unifiedRoot != "" {
		return RootInfo{
			Root:      unifiedRoot,
			Name:      unifiedName,
			RootSet:   roots,
			RootSetID: id,
			Actor:     parsedActor,
		}, nil
	}

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

// ResolveRepoName returns the name of the main repository directory.
func ResolveRepoName(workDir string) string {
	common, err := gitOne(workDir, "rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(workDir, common)
	}
	if filepath.Base(common) == ".git" {
		common = filepath.Dir(common)
	}
	return filepath.Base(common)
}

// ParseActor derives an actor name from a worktree directory basename per
// doc/collab/PROTOCOL.md §1. To generalize for other repositories (like deep-cuts),
// it strips R- and wt-R- prefixes where R is the repository name, falling back to wt- and botfam-.
func ParseActor(base string, repoName string) string {
	var actor string
	var prefixes []string
	if repoName != "" {
		prefixes = append(prefixes, "wt-"+repoName+"-", repoName+"-")
	}
	prefixes = append(prefixes, "wt-", "botfam-")

	matched := false
	for _, prefix := range prefixes {
		if strings.HasPrefix(base, prefix) {
			actor = strings.TrimPrefix(base, prefix)
			matched = true
			break
		}
	}
	if !matched {
		return ""
	}
	if actor == "" || validateName(actor) != nil {
		return ""
	}
	return actor
}

func validateName(name string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	if len(name) > 64 {
		return errors.New("name too long")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("invalid character %q in name", r)
		}
	}
	return nil
}

// GitObjectStores returns the absolute, symlink-resolved object store paths for
// workDir's repository, including alternates.
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

// RepoPath returns the absolute, symlink-resolved top-level of workDir's
// repository, falling back to the absolute workDir when it is not a git tree.
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

// ValidateHistoryPath rejects a history file path that is not absolute or that
// lives inside the git repository (history must be durable, out-of-repo state).
func ValidateHistoryPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("history file path must be absolute, got %q", path)
	}
	repoPath := RepoPath(".")
	if repoPath != "" {
		absRepo, err := filepath.Abs(repoPath)
		if err == nil {
			absPath, err := filepath.Abs(path)
			if err == nil {
				absPathClean := filepath.Clean(absPath)
				absRepoClean := filepath.Clean(absRepo)
				if absPathClean == absRepoClean || strings.HasPrefix(absPathClean, absRepoClean+string(filepath.Separator)) {
					return fmt.Errorf("history file path %q cannot be inside git repository %q", path, repoPath)
				}
			}
		}
	}
	return nil
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
