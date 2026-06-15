package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// defaultMintScopes matches tools/forge-login.sh: enough to open PRs, post
// reviews + commit status, merge, create issues/comments, and read+clear
// notifications. Override with --scopes for a narrower token.
const defaultMintScopes = "write:repository,write:issue,read:organization,read:user,read:misc,read:notification,write:notification"

// NewMintCmd builds `botfam mint` — the Go port of tools/forge-login.sh.
// It mints a forge access token for --user (password typed hidden, like passwd)
// and stores it at the per-harness path ~/.botfam/token-<harness>.
//
// Tokens are keyed by harness (the bot account is per-harness, shared across
// fams on a remote); the per-remote dimension (token-<harness>-<remote>) is a
// later addition — see wiki/proposal-unified-fam-config.
func NewMintCmd() *cobra.Command {
	var harness, user, forgeURL, scopesCSV, tokenFile string
	c := &cobra.Command{
		Use:           "mint --harness <harness> --user <forge-user> --forge-url <url>",
		Short:         "Mint a per-harness forge token (~/.botfam/token-<harness>)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if user == "" || forgeURL == "" {
				return fmt.Errorf("--user and --forge-url are required")
			}
			path := tokenFile
			if path == "" {
				if harness == "" {
					return fmt.Errorf("--harness is required (or pass --token-file)")
				}
				var err error
				if path, err = forge.HarnessTokenPath(harness); err != nil {
					return err
				}
			}
			// Read the password hidden, like passwd — never echoed, zeroed after use.
			fmt.Fprintf(cmd.ErrOrStderr(), "Forge password for %s: ", user)
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(cmd.ErrOrStderr())
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			tok, err := mintToken(forgeURL, user, string(pw), splitCSV(scopesCSV))
			for i := range pw {
				pw[i] = 0
			}
			if err != nil {
				return err
			}
			if err := writeTokenFile(path, tok); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote token for %s -> %s\n", user, path)
			return nil
		},
	}
	c.Flags().StringVar(&harness, "harness", "", "harness name; token lands at ~/.botfam/token-<harness>")
	c.Flags().StringVar(&user, "user", "", "forge username to mint the token for (e.g. claude-bot)")
	c.Flags().StringVar(&forgeURL, "forge-url", "", "HTTP(S) forge API base, e.g. http://gitea.home.rlupi.com:3000/")
	c.Flags().StringVar(&scopesCSV, "scopes", defaultMintScopes, "comma-separated token scopes")
	c.Flags().StringVar(&tokenFile, "token-file", "", "override the token output path (default ~/.botfam/token-<harness>)")
	return c
}

// mintToken POSTs to the Gitea/Forgejo token-creation API with basic auth and
// returns the new token's sha1. Token creation requires basic auth (not token
// auth), so the caller must supply the user's password.
func mintToken(forgeURL, user, password string, scopes []string) (string, error) {
	base := strings.TrimRight(forgeURL, "/")
	name := fmt.Sprintf("botfam-%s-%d", user, time.Now().Unix())
	body, err := json.Marshal(map[string]any{"name": name, "scopes": scopes})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/users/"+user+"/tokens", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(user, password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("token request to %s failed: %w", base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		return "", fmt.Errorf("forge rejected token request (%d): %s — check credentials/2FA, or retry with --scopes ''", resp.StatusCode, msg)
	}
	var out struct {
		Sha1 string `json:"sha1"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if out.Sha1 == "" {
		return "", fmt.Errorf("no token in forge response: %s", strings.TrimSpace(string(data)))
	}
	return out.Sha1, nil
}

// writeTokenFile writes the token mode 600 into ~/.botfam (dir 700), atomically.
func writeTokenFile(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
