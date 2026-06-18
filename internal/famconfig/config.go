package famconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/robertolupi/botfam/internal/gitexec"
)

// RepoConfig is a `[repo.<key>]` stanza in ~/.botfam/config.toml. It is matched
// by Path (the fam's local checkout parent dir) against the work dir, and its
// fields override the global defaults for any worktree under that path. Empty
// scalar fields fall through to the global value; Flags/Agents/Users merge
// key-by-key over the globals (see BuildRegistry).
type RepoConfig struct {
	Path            string                 `toml:"path"`
	Slug            string                 `toml:"slug,omitempty"`
	ForgeURL        string                 `toml:"forge_url,omitempty"`
	Repository      string                 `toml:"repository,omitempty"`
	TargetBranch    string                 `toml:"target_branch,omitempty"`
	ReleaseBranch   string                 `toml:"release_branch,omitempty"`
	Flags           map[string]any         `toml:"flags,omitempty"`
	Agents          map[string]AgentConfig `toml:"agent,omitempty"`
	Users           map[string]AgentConfig `toml:"user,omitempty"`
	WikiProjections []string               `toml:"wiki_projections,omitempty"`
}

// Config is the parsed ~/.botfam/config.toml: operator-owned global defaults, a
// global `[agent.<name>]`/`[user.<name>]` roster, and per-repo `[repo.<k>]`
// override stanzas. It replaces the per-fam fam.toml (#404). Resolve(workDir)
// merges defaults ⊕ globals ⊕ matching repo stanza ⊕ git-remote-derived into a
// Registry — the in-memory shape every consumer already uses, so callers don't
// change. The config file is the trust anchor: a work dir with no matching
// stanza is refused, never synthesized (fail-loud, #362).
type Config struct {
	ForgeURL      string                 `toml:"forge_url,omitempty"`
	TargetBranch  string                 `toml:"target_branch,omitempty"`
	ReleaseBranch string                 `toml:"release_branch,omitempty"`
	Flags         map[string]any         `toml:"flags,omitempty"`
	Agents        map[string]AgentConfig `toml:"agent,omitempty"`
	Users         map[string]AgentConfig `toml:"user,omitempty"`
	Repos         map[string]RepoConfig  `toml:"repo,omitempty"`

	// Secrets is the operator-owned `[secrets]` stanza: provider API keys (e.g.
	// GEMINI_API_KEY, OPENAI_API_KEY) used by `botfam external-review`, kept out
	// of the environment and shell history. Values are never logged. (#438)
	Secrets map[string]string `toml:"secrets,omitempty"`
}

// ConfigPath returns the global config file path: $BOTFAM_CONFIG when set (tests
// point it at a temp file), else ~/.botfam/config.toml.
func ConfigPath() (string, error) {
	if p := os.Getenv("BOTFAM_CONFIG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, ".botfam", "config.toml"), nil
}

// LoadConfig reads and parses the global config, backfilling each agent/user's
// Name (and IsUser) from its table key — globally and within every repo stanza.
func LoadConfig() (Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	backfillRoster(cfg.Agents, cfg.Users)
	for k, rc := range cfg.Repos {
		backfillRoster(rc.Agents, rc.Users)
		cfg.Repos[k] = rc
	}
	return cfg, nil
}

// LoadOrInitConfig loads the config, returning an empty Config (not an error)
// when the file does not yet exist — for write commands that create it.
func LoadOrInitConfig() (Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	return cfg, nil
}

// WriteConfig atomically writes cfg as TOML to the global config path.
func WriteConfig(cfg Config) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal botfam config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// UpsertRepo sets (or replaces) the `[repo.<key>]` stanza.
func (cfg *Config) UpsertRepo(key string, rc RepoConfig) {
	if cfg.Repos == nil {
		cfg.Repos = map[string]RepoConfig{}
	}
	cfg.Repos[key] = rc
}

func backfillRoster(agents, users map[string]AgentConfig) {
	for k, ac := range agents {
		ac.Name = k
		ac.IsUser = false
		agents[k] = ac
	}
	for k, ac := range users {
		ac.Name = k
		ac.IsUser = true
		users[k] = ac
	}
}

// MatchRepo finds the `[repo.<k>]` stanza whose Path is the longest ancestor (or
// equal) of workDir, after expanding `~` and resolving symlinks on both sides.
// It replaces FindFamTOMLPath's walk-up: every worktree under a fam dir matches
// the one stanza keyed by that dir. Returns ok=false when nothing matches.
func MatchRepo(cfg Config, workDir string) (string, RepoConfig, bool) {
	cand := ExpandPath(workDir)
	if cand == "" {
		return "", RepoConfig{}, false
	}
	var bestKey string
	var bestRC RepoConfig
	bestLen := -1
	for k, rc := range cfg.Repos {
		base := ExpandPath(rc.Path)
		if base == "" {
			continue
		}
		if cand == base || strings.HasPrefix(cand, base+string(filepath.Separator)) {
			if len(base) > bestLen {
				bestLen = len(base)
				bestKey = k
				bestRC = rc
			}
		}
	}
	if bestLen < 0 {
		return "", RepoConfig{}, false
	}
	return bestKey, bestRC, true
}

// BuildRegistry merges global defaults ⊕ the matched repo stanza ⊕ git-remote
// derivation into a Registry. Scalar repo fields override globals when non-empty;
// Flags/Agents/Users merge key-by-key. Repository falls back to the git remote
// so consumers that require it (ingest) still get a value.
func BuildRegistry(cfg Config, key string, rc RepoConfig, workDir string) Registry {
	reg := Registry{
		Name:              key,
		Slug:              firstNonEmpty(rc.Slug, key),
		ForgeURL:          firstNonEmpty(rc.ForgeURL, cfg.ForgeURL),
		IntegrationBranch: firstNonEmpty(rc.TargetBranch, cfg.TargetBranch),
		ReleaseBranch:     firstNonEmpty(rc.ReleaseBranch, cfg.ReleaseBranch),
		Repository:        rc.Repository,
		Flags:             mergeFlags(cfg.Flags, rc.Flags),
		Agents:            mergeRoster(cfg.Agents, rc.Agents, false),
		Users:             mergeRoster(cfg.Users, rc.Users, true),
		WikiProjections:   rc.WikiProjections,
		RepoPaths:         []string{ExpandPath(rc.Path)},
	}
	if reg.Repository == "" {
		reg.Repository = RemoteRepository(workDir)
	}
	return reg
}

// ResolveConfig is the single config-backed resolution: load ~/.botfam/config.toml,
// match the repo stanza for workDir, and build the merged Registry. It fails loud
// when the file is unreadable or no stanza matches — the #404 replacement for the
// per-fam fam.toml read, preserving the fail-loud invariant (#362).
func ResolveConfig(workDir string) (Registry, error) {
	path, _ := ConfigPath()
	cfg, err := LoadConfig()
	if err != nil {
		return Registry{}, fmt.Errorf("no readable botfam config at %s: create it or run `botfam setup`; if it persists, report to your operator (%v)", path, err)
	}
	key, rc, ok := MatchRepo(cfg, workDir)
	if !ok {
		return Registry{}, fmt.Errorf("work dir %s is not registered in %s: add a [repo.<name>] stanza whose path is an ancestor of it (or run `botfam setup`); report to your operator", workDir, path)
	}
	return BuildRegistry(cfg, key, rc, workDir), nil
}

// RemoteRepository derives "owner/repo" from workDir's git remote, trying
// $BOTFAM_FORGE_REMOTE, then "gitea", then "origin". Returns "" when none parse.
func RemoteRepository(workDir string) string {
	var remotes []string
	if r := os.Getenv("BOTFAM_FORGE_REMOTE"); r != "" {
		remotes = append(remotes, r)
	}
	remotes = append(remotes, "gitea", "origin")
	for _, r := range remotes {
		url, err := gitexec.One(workDir, "config", "--get", "remote."+r+".url")
		if err != nil || url == "" {
			continue
		}
		if _, owner, repo, err := ParseGitRemoteURL(url); err == nil && owner != "" && repo != "" {
			return owner + "/" + repo
		}
	}
	return ""
}

// SplitOwnerRepo splits an "owner/repo" value, reporting ok only for a
// well-formed two-part value. (Moved from internal/forge so the dependency-free
// leaf owns it; forge re-uses it.)
func SplitOwnerRepo(repository string) (owner, repo string, ok bool) {
	parts := strings.Split(repository, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

// ParseGitRemoteURL parses a git remote URL (HTTP(S), ssh://, or scp-like) into
// the forge API base, owner, and repo. (Moved from internal/forge so famconfig —
// the leaf — can derive Repository without importing forge.)
func ParseGitRemoteURL(rawURL string) (baseURL, owner, repo string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", "", errors.New("empty remote URL")
	}
	rawURL = strings.TrimSuffix(rawURL, ".git")

	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		parts := strings.Split(rawURL, "/")
		if len(parts) < 5 {
			return "", "", "", fmt.Errorf("invalid HTTP remote URL format: %q", rawURL)
		}
		repo = parts[len(parts)-1]
		owner = parts[len(parts)-2]
		baseURL = strings.Join(parts[:len(parts)-2], "/") + "/"
		return baseURL, owner, repo, nil
	}

	if strings.HasPrefix(rawURL, "ssh://") {
		trimmed := strings.TrimPrefix(rawURL, "ssh://")
		slashIdx := strings.Index(trimmed, "/")
		if slashIdx == -1 {
			return "", "", "", fmt.Errorf("invalid ssh remote URL format: %q", rawURL)
		}
		pathPart := trimmed[slashIdx+1:]
		parts := strings.Split(pathPart, "/")
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("invalid ssh remote URL path format: %q", pathPart)
		}
		owner = parts[0]
		repo = parts[1]

		hostPart := trimmed[:slashIdx]
		if idx := strings.Index(hostPart, "@"); idx != -1 {
			hostPart = hostPart[idx+1:]
		}
		if idx := strings.Index(hostPart, ":"); idx != -1 {
			hostPart = hostPart[:idx]
		}
		if hostPart == "gitea" {
			baseURL = "http://gitea:3000/"
		} else {
			baseURL = fmt.Sprintf("https://%s/", hostPart)
		}
		return baseURL, owner, repo, nil
	}

	if strings.Contains(rawURL, ":") {
		parts := strings.SplitN(rawURL, ":", 2)
		hostPart := parts[0]
		pathPart := parts[1]

		if idx := strings.Index(hostPart, "@"); idx != -1 {
			hostPart = hostPart[idx+1:]
		}

		pathParts := strings.Split(pathPart, "/")
		if len(pathParts) != 2 {
			return "", "", "", fmt.Errorf("invalid SCP-like remote URL path: %q", pathPart)
		}
		owner = pathParts[0]
		repo = pathParts[1]

		if hostPart == "gitea" {
			baseURL = "http://gitea:3000/"
		} else {
			baseURL = fmt.Sprintf("https://%s/", hostPart)
		}
		return baseURL, owner, repo, nil
	}

	return "", "", "", fmt.Errorf("unrecognized git remote URL format: %q", rawURL)
}

// --- merge helpers -----------------------------------------------------------

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mergeFlags(global, override map[string]any) map[string]any {
	if len(global) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]any, len(global)+len(override))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeRoster overlays per-repo agent/user overrides onto the global roster,
// field-by-field (non-empty override fields win; flags merge key-by-key).
func mergeRoster(global, override map[string]AgentConfig, isUser bool) map[string]AgentConfig {
	if len(global) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]AgentConfig, len(global)+len(override))
	for k, v := range global {
		v.Name = k
		v.IsUser = isUser
		out[k] = v
	}
	for k, ov := range override {
		ov.Name = k
		ov.IsUser = isUser
		if base, ok := out[k]; ok {
			out[k] = mergeAgent(base, ov)
		} else {
			out[k] = ov
		}
	}
	return out
}

func mergeAgent(base, ov AgentConfig) AgentConfig {
	out := base
	if ov.Harness != "" {
		out.Harness = ov.Harness
	}
	if ov.ForgeUser != "" {
		out.ForgeUser = ov.ForgeUser
	}
	if ov.Email != "" {
		out.Email = ov.Email
	}
	out.Flags = mergeFlags(base.Flags, ov.Flags)
	return out
}

// ExpandPath expands a leading ~, absolutizes, and resolves symlinks, returning
// a cleaned path. On any failure it falls back to the cleaned absolute form.
func ExpandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		p = eval
	}
	return filepath.Clean(p)
}
