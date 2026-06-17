package famconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/robertolupi/botfam/internal/gitexec"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver resolves a worktree's fam identity (root set included). GitResolver is
// the production implementation; tests inject a fake — typically via FuncResolver
// — so they can control the resolved identity without manipulating the process
// environment (#334). famctx.Resolve consumes this interface: it is a live seam,
// not a decorative one.
type Resolver interface {
	ResolveIdentity(workDir string) (RootInfo, error)
}

// FuncResolver adapts a plain function to the Resolver interface — the injection
// seam tests use to return a canned RootInfo instead of standing up a git repo
// and env.
type FuncResolver func(workDir string) (RootInfo, error)

// ResolveIdentity implements Resolver.
func (f FuncResolver) ResolveIdentity(workDir string) (RootInfo, error) { return f(workDir) }

// GitResolver derives a fam Root/Name/Actor for a worktree. It is the dependency-free
// home of identity resolution (#311): both internal/cli and internal/mcp resolve
// through it without importing each other.
type GitResolver struct {
	Env []string
}

// Compile-time checks that both resolvers satisfy the interface.
var (
	_ Resolver = GitResolver{}
	_ Resolver = FuncResolver(nil)
)

// RootInfo is the resolved fam root for a worktree.
type RootInfo struct {
	FamIdentity
	RootSet   []string
	RootSetID string
}

// ResolveIdentity resolves the full git-specific identity including root set.
// Identity is now config-backed (#404): the fam dir is the parent of the git
// worktree top-level, and the fam Name/Actor/role come from the matched
// `[repo.<k>]` stanza in ~/.botfam/config.toml. There is no home-dir fam
// synthesis: when no stanza matches, Name falls back to the fam-dir basename and
// the role stays Unknown (permissive callers handle that; the strict ResolveFam
// path fails loud via ResolveConfig).
func (r GitResolver) ResolveIdentity(workDir string) (RootInfo, error) {
	repoName := ResolveRepoName(workDir)
	var parsedActor string
	var famDir, name string
	var gitRoot string
	var haveReg bool
	var reg Registry

	if absDir, err := filepath.Abs(workDir); err == nil {
		if evalDir, err := filepath.EvalSymlinks(absDir); err == nil {
			absDir = evalDir
		}
		gitRoot, _ = gitexec.One(absDir, "rev-parse", "--show-toplevel")
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
		if gitRoot != "" {
			famDir = filepath.Dir(gitRoot)
			// Bare-name worktrees (the wt- prefix is retired): accept the
			// worktree-root basename when it is a declared [agent.<name>]/
			// [user.<name>] in the matched config stanza.
			if cfg, err := LoadConfig(); err == nil {
				if key, rc, ok := MatchRepo(cfg, workDir); ok {
					reg = BuildRegistry(cfg, key, rc, workDir)
					haveReg = true
					name = reg.Name
					base := filepath.Base(gitRoot)
					if _, ok := reg.Agents[base]; ok {
						parsedActor = base
					} else if _, ok := reg.Users[base]; ok {
						parsedActor = base
					}
				}
			}
			if name == "" {
				name = filepath.Base(famDir)
			}
		}
	}

	roots, err := gitexec.Lines(workDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return RootInfo{}, makeNoGitHistoryError()
	}
	sort.Strings(roots)
	sum := sha256.Sum256([]byte(strings.Join(roots, "\n")))
	id := hex.EncodeToString(sum[:])[:12]

	role := RoleUnknown
	if haveReg {
		if _, ok := reg.Agents[parsedActor]; ok {
			role = RoleAgent
		} else if _, ok := reg.Users[parsedActor]; ok {
			role = RoleUser
		} else if gitRoot != "" && gitRoot == famDir {
			role = RoleBase
		}
	}

	cfgPath, _ := ConfigPath()
	return RootInfo{
		FamIdentity: FamIdentity{
			FamDir:      famDir,
			FamTOMLPath: cfgPath,
			Name:        name,
			Actor:       parsedActor,
			ActorRole:   role,
			Source:      SourceWorkDir,
		},
		RootSet:   roots,
		RootSetID: id,
	}, nil
}

// ResolveRepoName returns the name of the main repository directory.
func ResolveRepoName(workDir string) string {
	common, err := gitexec.One(workDir, "rev-parse", "--git-common-dir")
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
	common, err := gitexec.One(workDir, "rev-parse", "--git-common-dir")
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
	if top, err := gitexec.One(workDir, "rev-parse", "--show-toplevel"); err == nil {
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
	generic := errors.New("not inside a fam worktree and no git history could be used to derive a fam root; run from a member worktree or run botfam setup")
	cfg, err := LoadConfig()
	if err != nil || len(cfg.Repos) == 0 {
		return generic
	}

	var sb strings.Builder
	sb.WriteString("not inside a fam worktree and no git history could be used to derive a fam root.\n")
	sb.WriteString("To fix this, run from inside a member worktree or run 'botfam setup'.\n\n")
	sb.WriteString("Configured fams in ~/.botfam/config.toml:\n")
	for key, rc := range cfg.Repos {
		fmt.Fprintf(&sb, "  - %s (path %s)\n", key, rc.Path)
	}
	return errors.New(strings.TrimSuffix(sb.String(), "\n"))
}
