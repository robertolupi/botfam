package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/robertolupi/botfam/internal/gitexec"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds `botfam doctor` — environment self-diagnosis. Each check
// reports ok/warn/fail with a remediation hint and the command exits non-zero
// if any check fails, so an agent or operator can see what's wrong (and how to
// fix it) instead of debugging by hand. Checks: forge credential identity (the
// push-authentication leak, #150) and git author identity (the commit-authorship
// audit, #157). Part of observability #144.
func NewDoctorCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "doctor",
		Short:         "Diagnose the agent's environment (forge credential + git author identity, …)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			checks := []doctorCheck{
				credentialHelperCheck(wd),
				gitIdentityCheck(wd),
			}
			failed := false
			for _, ch := range checks {
				fmt.Fprintf(out, "%s %s\n", statusIcon(ch.status), ch.name)
				if ch.detail != "" {
					fmt.Fprintf(out, "   %s\n", ch.detail)
				}
				if ch.fix != "" {
					fmt.Fprintf(out, "   fix: %s\n", ch.fix)
				}
				if ch.status == doctorFail {
					failed = true
				}
			}
			if failed {
				return errors.New("doctor: one or more checks failed")
			}
			return nil
		},
	}
	return c
}

const (
	doctorOK   = "ok"
	doctorWarn = "warn"
	doctorFail = "fail"
)

type doctorCheck struct {
	name   string
	status string
	detail string
	fix    string
}

func statusIcon(status string) string {
	switch status {
	case doctorOK:
		return "✅"
	case doctorFail:
		return "❌"
	default:
		return "⚠️"
	}
}

// credentialHelperCheck verifies that no inherited credential helper
// (osxkeychain, the global store/cache, etc.) can answer the forge credential
// challenge before botfam's. An inherited helper means `git push` authenticates
// as whatever account it has cached — mis-attributing every agent's push (#150).
func credentialHelperCheck(workDir string) doctorCheck {
	const name = "forge credential identity"
	url, err := forgeRemoteURL(workDir)
	if err != nil || url == "" {
		return doctorCheck{name, doctorWarn, "could not resolve a forge remote URL", "configure a `gitea` or `origin` remote"}
	}
	raw, values, err := gitCredentialHelpers(workDir, url)
	if err != nil {
		return doctorCheck{name, doctorWarn, fmt.Sprintf("could not read credential.helper for %s: %v", url, err), ""}
	}
	offending := offendingHelpers(values)
	if len(offending) > 0 {
		return doctorCheck{
			name, doctorFail,
			fmt.Sprintf("inherited helper(s) can answer for %s: %s\n   %s",
				url, strings.Join(offending, ", "), strings.ReplaceAll(raw, "\n", "\n   ")),
			`clear inherited helpers in the worktree — ` +
				"`git config --local credential.helper \"\"` before configuring " +
				"`botfam credential` (run `botfam setup` / `tools/forge-setup.sh`)",
		}
	}
	if len(values) == 0 {
		return doctorCheck{name, doctorWarn,
			fmt.Sprintf("no credential helper configured for %s", url),
			"run `botfam setup` / `tools/forge-setup.sh` to configure `botfam credential`"}
	}
	return doctorCheck{name, doctorOK, fmt.Sprintf("effective helper(s) for %s: %s", url, strings.Join(values, ", ")), ""}
}

// gitIdentityCheck is the audit half of the push-attribution story (#157,
// follow-on to #152): the credential-helper check covers *push authentication*,
// this covers *commit authorship*. Per docs/protocol §5 each actor must set
// `git config --worktree user.name <actor>`; if a worktree never set its
// identity, commits silently inherit the global/shared user.name and are
// mis-attributed even when the push helper is correct.
func gitIdentityCheck(workDir string) doctorCheck {
	var actor string
	if info, err := (GitResolver{}).ResolveIdentity(workDir); err == nil {
		actor = info.Actor
	}
	name, _ := gitexec.One(workDir, "config", "user.name")
	email, _ := gitexec.One(workDir, "config", "user.email")
	return evaluateGitIdentity(actor, name, email)
}

// evaluateGitIdentity is the pure decision behind gitIdentityCheck: given the
// resolved actor and the effective git author identity, decide ok/warn/fail.
func evaluateGitIdentity(actor, name, email string) doctorCheck {
	const checkName = "git author identity"
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	switch {
	case name == "":
		return doctorCheck{checkName, doctorFail,
			"no git user.name configured — commits will inherit the global/shared identity and be mis-attributed",
			gitIdentityFix(actor)}
	case actor != "" && name != actor:
		return doctorCheck{checkName, doctorWarn,
			fmt.Sprintf("git user.name %q does not match the resolved actor %q", name, actor),
			gitIdentityFix(actor)}
	case email == "":
		return doctorCheck{checkName, doctorWarn,
			fmt.Sprintf("git user.name is %q but user.email is unset", name),
			gitIdentityFix(actor)}
	default:
		return doctorCheck{checkName, doctorOK,
			fmt.Sprintf("commits authored as %s <%s>", name, email), ""}
	}
}

// gitIdentityFix is the remediation hint for a missing/mismatched worktree
// identity. It names the resolved actor when known, else a placeholder.
func gitIdentityFix(actor string) string {
	who := actor
	if who == "" {
		who = "<actor>"
	}
	return fmt.Sprintf("set the worktree identity — `git config --worktree user.name %s` (and user.email)", who)
}

// offendingHelpers returns the configured helpers that are NOT botfam's — any
// inherited helper that could answer the forge credential challenge. An empty
// value is a reset directive, not a helper, so it is ignored.
func offendingHelpers(values []string) []string {
	var bad []string
	for _, h := range values {
		h = strings.TrimSpace(h)
		if h == "" || strings.Contains(h, "botfam") {
			continue
		}
		bad = append(bad, h)
	}
	return bad
}

// parseCredentialHelpers extracts the helper values from `git config` output.
// It accepts both plain `--get-urlmatch` lines (bare values) and `--show-origin`
// lines (`<origin>\t<value>`), taking the text after the last tab when present.
func parseCredentialHelpers(showOriginOutput string) []string {
	var values []string
	for _, line := range strings.Split(showOriginOutput, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		val := line
		if i := strings.LastIndex(line, "\t"); i >= 0 {
			val = line[i+1:]
		}
		values = append(values, strings.TrimSpace(val))
	}
	return values
}

// forgeRemoteURL resolves the forge remote URL, preferring the botfam `gitea`
// remote, falling back to `origin`.
func forgeRemoteURL(workDir string) (string, error) {
	for _, remote := range []string{"gitea", "origin"} {
		out, err := gitexec.One(workDir, "remote", "get-url", remote)
		if err == nil {
			if u := strings.TrimSpace(out); u != "" {
				return u, nil
			}
		}
	}
	return "", errors.New("no gitea/origin remote configured")
}

// gitCredentialHelpers returns the helper values effective for url (analysis)
// and a human-readable origin listing (display). git rejects `--show-origin`
// together with `--get-urlmatch`, so the two are fetched separately: the
// URL-scoped, post-reset effective values via `--get-urlmatch`, and the
// (non-URL-scoped) origins via `--show-origin --get-all` for context. A git
// exit code of 1 means "no matching config" — not an error, just no helpers.
func gitCredentialHelpers(workDir, url string) (origins string, values []string, err error) {
	vcmd := exec.Command("git", "config", "--get-urlmatch", "credential.helper", url)
	vcmd.Dir = workDir
	var vout, verr bytes.Buffer
	vcmd.Stdout = &vout
	vcmd.Stderr = &verr
	if runErr := vcmd.Run(); runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) && ee.ExitCode() == 1 {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("%v: %s", runErr, strings.TrimSpace(verr.String()))
	}
	values = parseCredentialHelpers(strings.TrimRight(vout.String(), "\n"))

	// Origins are best-effort context for the human; never fatal.
	rcmd := exec.Command("git", "config", "--show-origin", "--get-all", "credential.helper")
	rcmd.Dir = workDir
	var rout bytes.Buffer
	rcmd.Stdout = &rout
	_ = rcmd.Run()
	origins = strings.TrimRight(rout.String(), "\n")
	return origins, values, nil
}
