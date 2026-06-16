package famconfig

import (
	"os"
	"strings"
)

// Canonical harness identifiers. These are the names every part of botfam keys
// per-harness artifacts on (the token path, the MCPConfigurator implementations,
// `clone`'s default, `init`'s fam.toml template). A fam.toml `harness` field, an
// MCP client's clientInfo.name, and an env signal all resolve onto one of these.
const (
	HarnessClaudeCode  = "claude-code"
	HarnessCodex       = "codex"
	HarnessAntigravity = "antigravity"
)

// harnessSpec is the single source of truth for one harness: its canonical name
// plus every signal that should resolve to it. Previously this knowledge was
// scattered as bare string literals across clone's default, each
// MCPConfigurator.Harness, and ad-hoc fam.toml values, which let the same harness
// be spelled two ways and silently diverge (#371).
type harnessSpec struct {
	// canonical is the one true name.
	canonical string
	// aliases are non-canonical fam.toml spellings that mean this harness. `clone`
	// maps the agent named "claude" onto harness "claude-code", so a fam.toml that
	// writes harness = 'claude' means Claude Code, not a separate harness.
	aliases []string
	// envVars: if ANY of these is present (non-empty) in the process environment,
	// the process is running under this harness. botfam (the CLI and the `serve`
	// MCP server) runs as a child of the harness and inherits its environment, so
	// this is a reliable runtime signal. Only signals verified on a live harness
	// are listed; unverified harnesses leave this empty rather than guess.
	envVars []string
	// clientNameSubstrings are lowercased substrings matched against the MCP
	// initialize clientInfo.name. This is the protocol-native signal but is only
	// available inside a live `serve` session.
	clientNameSubstrings []string
}

// harnessRegistry enumerates the known harnesses. Order matters only for env
// detection (first match wins); the sets are otherwise disjoint.
var harnessRegistry = []harnessSpec{
	{
		canonical:            HarnessClaudeCode,
		aliases:              []string{"claude"},
		envVars:              []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"},
		clientNameSubstrings: []string{"claude"},
	},
	{
		canonical:            HarnessCodex,
		clientNameSubstrings: []string{"codex"},
	},
	{
		canonical:            HarnessAntigravity,
		clientNameSubstrings: []string{"antigravity", "gemini"},
	},
}

// CanonicalHarness folds a declared (fam.toml) harness name onto its canonical
// form, resolving known aliases (e.g. "claude" -> "claude-code"). Unknown names
// pass through unchanged. Idempotent.
func CanonicalHarness(harness string) string {
	if harness == "" {
		return ""
	}
	for _, spec := range harnessRegistry {
		if harness == spec.canonical {
			return spec.canonical
		}
		for _, a := range spec.aliases {
			if harness == a {
				return spec.canonical
			}
		}
	}
	return harness
}

// DetectHarnessFromEnv returns the canonical harness implied by environ (an
// os.Environ()-style "K=V" slice), or "" if none matches. nil means the process
// environment.
func DetectHarnessFromEnv(environ []string) string {
	if environ == nil {
		environ = os.Environ()
	}
	present := func(key string) bool {
		prefix := key + "="
		for _, kv := range environ {
			if strings.HasPrefix(kv, prefix) {
				return strings.TrimPrefix(kv, prefix) != ""
			}
		}
		return false
	}
	for _, spec := range harnessRegistry {
		for _, key := range spec.envVars {
			if present(key) {
				return spec.canonical
			}
		}
	}
	return ""
}

// DetectHarnessFromClientName returns the canonical harness implied by an MCP
// client's clientInfo.name (matched case-insensitively against known
// substrings), or "" if none matches.
func DetectHarnessFromClientName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	for _, spec := range harnessRegistry {
		for _, sub := range spec.clientNameSubstrings {
			if strings.Contains(n, sub) {
				return spec.canonical
			}
		}
	}
	return ""
}

// HarnessResolution is the outcome of reconciling a declared harness with the
// runtime-detected one.
type HarnessResolution struct {
	// Declared is the canonicalized fam.toml value (may be "").
	Declared string
	// Detected is the runtime-detected harness from clientName/env (may be "").
	Detected string
	// Effective is the harness to actually use: Detected when available, else
	// Declared. This is what per-harness artifacts (the token path) key on.
	Effective string
	// Source names where Effective came from: "clientinfo", "env", or "declared".
	Source string
	// Mismatch is true when both a declared and a detected harness exist and they
	// disagree — a misconfigured fam.toml worth surfacing.
	Mismatch bool
}

// ResolveHarness reconciles the declared (fam.toml) harness with runtime signals.
// The runtime is authoritative because per-harness forge tokens follow the
// harness actually running, not whatever a fam happened to declare (#371):
// detection wins, the declaration is the fallback. clientName is the MCP
// initialize clientInfo.name ("" outside a live serve session); environ is an
// os.Environ()-style slice (nil = the process environment).
func ResolveHarness(declared, clientName string, environ []string) HarnessResolution {
	res := HarnessResolution{Declared: CanonicalHarness(declared)}

	if h := DetectHarnessFromClientName(clientName); h != "" {
		res.Detected = h
	} else if h := DetectHarnessFromEnv(environ); h != "" {
		res.Detected = h
	}

	switch {
	case res.Detected != "":
		res.Effective = res.Detected
		if DetectHarnessFromClientName(clientName) != "" {
			res.Source = "clientinfo"
		} else {
			res.Source = "env"
		}
		res.Mismatch = res.Declared != "" && res.Declared != res.Detected
	default:
		res.Effective = res.Declared
		res.Source = "declared"
	}
	return res
}
