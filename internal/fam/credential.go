package fam

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// NewCredentialCmd builds `botfam credential` — a git credential helper that
// answers the fam's forge with the per-harness token, replacing the
// tools/git-credential-botfam shell shim (#212). Moving it into the Go binary
// means it no longer depends on a script path inside a particular worktree
// (the cutover hazard: an agent's helper pointing at a to-be-retired tree) and
// `botfam setup` can wire it.
//
// git invokes it as `botfam credential <get|store|erase>` with the request on
// stdin (key=value lines, blank line terminates) and reads the answer on
// stdout. Only `get` is implemented: tokens are minted by `botfam mint`, never
// by git, so `store`/`erase` are no-ops. It answers ONLY for the fam's forge
// host, so the token is never offered to github.com or any other host even
// when installed as a global `credential.helper`.
//
// Wire it (scoped to the forge host) via:
//
//	git config --global "credential.<forge-url>.helper" "!/abs/path/botfam credential"
func NewCredentialCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "credential <get|store|erase>",
		Short: "Git credential helper: answer the fam's forge with the per-harness token",
		Long: `git credential helper for the fam's forge.

git invokes "botfam credential get" (request on stdin) during an HTTP forge
push/fetch; this answers with the bot user + per-harness token from the unified
fam.toml. It self-restricts to the fam's forge host, so the token is never
offered to other hosts. store/erase are no-ops (tokens come from "botfam mint").

Configure (scoped to the forge host):
  git config --global "credential.<forge-url>.helper" "!/abs/path/botfam credential"`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			op := ""
			if len(args) > 0 {
				op = args[0]
			}
			wd, err := os.Getwd()
			if err != nil {
				// Can't introspect the worktree; stay silent so git falls
				// through to its other helpers rather than wedging the push.
				return nil
			}
			return runCredential(op, wd, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// runCredential implements the git credential protocol for the `get` op. Any
// condition where we cannot legitimately answer (not an agent worktree, a
// different host, a missing token) returns nil WITHOUT writing to stdout, so
// git falls through to its other helpers. This silent fall-through is the
// credential-helper exception to botfam's fail-loud rule: this code runs as git
// plumbing for *every* host (including github.com), and erroring would break
// unrelated git operations. To stay debuggable, a host that matches the forge
// but has no usable token still writes a one-line diagnostic to stderr.
//
// The BOTFAM_FORGE_HOST / BOTFAM_FORGE_USER / BOTFAM_TOKEN_FILE env vars
// override the corresponding fam-resolved values (the same overrides the
// retired shell helper documented); when all three are set we answer without
// reading fam.toml, which is how the hermetic integration test pushes as
// arbitrary identities from a non-agent checkout.
func runCredential(op, workDir string, in io.Reader, out, errOut io.Writer) error {
	if op != "get" {
		return nil // store/erase/unknown: nothing to do
	}

	reqProto, reqHost := parseCredentialRequest(in)

	forgeHost := os.Getenv("BOTFAM_FORGE_HOST")
	user := os.Getenv("BOTFAM_FORGE_USER")
	tokenPath := os.Getenv("BOTFAM_TOKEN_FILE")

	// Fill any field the env didn't supply from the resolved fam identity.
	// Skip ResolveFam entirely only when the env fully specifies the answer.
	if forgeHost == "" || user == "" || tokenPath == "" {
		rf, err := ResolveFam(workDir)
		if err != nil {
			return nil // not an agent worktree and env didn't fully specify identity
		}
		if forgeHost == "" {
			forgeHost = forgeHostFromURL(rf.ForgeURL)
		}
		if user == "" {
			if user = rf.Agent.ForgeUser; user == "" {
				user = rf.Actor + "-bot"
			}
		}
		if tokenPath == "" {
			tokenPath = rf.TokenPath
		}
	}

	if forgeHost == "" || !credentialHostMatches(reqHost, forgeHost) {
		return nil // not our forge host: never offer the token here
	}

	token, err := readTokenFile(tokenPath)
	if err != nil {
		fmt.Fprintf(errOut, "botfam credential: no usable token at %s for %s (%v); run `botfam mint`\n", tokenPath, forgeHost, err)
		return nil
	}

	var b strings.Builder
	if reqProto != "" {
		fmt.Fprintf(&b, "protocol=%s\n", reqProto)
	}
	fmt.Fprintf(&b, "host=%s\n", reqHost)
	fmt.Fprintf(&b, "username=%s\n", user)
	fmt.Fprintf(&b, "password=%s\n", token)
	_, err = io.WriteString(out, b.String())
	return err
}

// parseCredentialRequest reads git's key=value request (blank line terminates)
// and returns the protocol and host fields; all other keys are ignored.
func parseCredentialRequest(in io.Reader) (protocol, host string) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "protocol":
			protocol = v
		case "host":
			host = v
		}
	}
	return protocol, host
}

// forgeHostFromURL extracts host[:port] from a forge URL like
// "http://gitea:3000/". Returns "" if the URL is empty or unparseable.
func forgeHostFromURL(forgeURL string) string {
	if forgeURL == "" {
		return ""
	}
	u, err := url.Parse(forgeURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// credentialHostMatches reports whether git's requested host matches the forge
// host, accepting both the host[:port] form and the bare hostname (git omits a
// default port).
func credentialHostMatches(reqHost, forgeHost string) bool {
	if reqHost == "" {
		return false
	}
	if reqHost == forgeHost {
		return true
	}
	if i := strings.IndexByte(forgeHost, ':'); i != -1 {
		return reqHost == forgeHost[:i]
	}
	return false
}

// readTokenFile reads and trims a token file, erroring on an empty token.
func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("token file is empty")
	}
	return token, nil
}
