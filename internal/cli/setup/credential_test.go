package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCredentialRequest(t *testing.T) {
	in := "protocol=http\nhost=gitea:3000\npath=botfam/botfam.git\nusername=ignored\n\nafter=ignored\n"
	proto, host := parseCredentialRequest(strings.NewReader(in))
	if proto != "http" {
		t.Errorf("protocol = %q, want http", proto)
	}
	if host != "gitea:3000" {
		t.Errorf("host = %q, want gitea:3000", host)
	}
}

func TestForgeHostFromURL(t *testing.T) {
	cases := map[string]string{
		"http://gitea:3000/":                 "gitea:3000",
		"https://gitea.example.com:3000/": "gitea.example.com:3000",
		"http://forge.example.com/":          "forge.example.com",
		"":                                   "",
		"://bad":                             "",
	}
	for in, want := range cases {
		if got := forgeHostFromURL(in); got != want {
			t.Errorf("forgeHostFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCredentialHostMatches(t *testing.T) {
	cases := []struct {
		reqHost, forgeHost string
		want               bool
	}{
		{"gitea:3000", "gitea:3000", true}, // exact host:port
		{"gitea", "gitea:3000", true},      // git omits the port → bare host
		{"gitea:3000", "gitea", false},     // forge has no port; req must match exactly
		{"gitea", "gitea", true},           // both bare
		{"evil.com", "gitea:3000", false},  // different host
		{"gitea.evil.com", "gitea:3000", false},
		{"", "gitea:3000", false}, // empty request never matches
	}
	for _, c := range cases {
		if got := credentialHostMatches(c.reqHost, c.forgeHost); got != c.want {
			t.Errorf("credentialHostMatches(%q,%q) = %v, want %v", c.reqHost, c.forgeHost, got, c.want)
		}
	}
}

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "tok")
	if err := os.WriteFile(good, []byte("  sha1token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readTokenFile(good); err != nil || got != "sha1token" {
		t.Errorf("readTokenFile(good) = %q,%v; want sha1token,nil", got, err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTokenFile(empty); err == nil {
		t.Error("readTokenFile(empty) should error")
	}

	if _, err := readTokenFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("readTokenFile(missing) should error")
	}
}

// credentialFixture sets HOME to a temp dir with the per-harness token in place
// and returns a git-inited claude agent worktree under a fam.toml whose
// forge_url is gitea.example.com:3000 (matching sampleFamTOML).
func credentialFixture(t *testing.T, token string) (wt string) {
	t.Helper()
	// Ensure the env-override path can't leak in from the ambient environment;
	// these tests exercise the fam.toml-resolved path.
	t.Setenv("BOTFAM_FORGE_HOST", "")
	t.Setenv("BOTFAM_FORGE_USER", "")
	t.Setenv("BOTFAM_TOKEN_FILE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if token != "" {
		botfamDir := filepath.Join(home, ".botfam")
		if err := os.MkdirAll(botfamDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(botfamDir, "token-claude-code"), []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	famDir := resolveFamFixture(t)
	wt = filepath.Join(famDir, "claude")
	gitInit(t, wt)
	return wt
}

func TestRunCredentialGetAnswersForge(t *testing.T) {
	wt := credentialFixture(t, "deadbeeftoken")
	var out, errOut strings.Builder
	req := strings.NewReader("protocol=https\nhost=gitea.example.com:3000\n\n")
	if err := runCredential("get", wt, req, &out, &errOut); err != nil {
		t.Fatalf("runCredential: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"protocol=https\n",
		"host=gitea.example.com:3000\n",
		"username=claude-bot\n",
		"password=deadbeeftoken\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected stderr: %s", errOut.String())
	}
}

func TestRunCredentialGetBareHostMatch(t *testing.T) {
	wt := credentialFixture(t, "tok")
	var out, errOut strings.Builder
	// git omits the default-looking port and sends the bare host.
	req := strings.NewReader("protocol=https\nhost=gitea.example.com\n\n")
	if err := runCredential("get", wt, req, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "password=tok\n") {
		t.Errorf("bare-host request should be answered; got:\n%s", out.String())
	}
}

func TestRunCredentialIgnoresOtherHost(t *testing.T) {
	wt := credentialFixture(t, "tok")
	var out, errOut strings.Builder
	req := strings.NewReader("protocol=https\nhost=github.com\n\n")
	if err := runCredential("get", wt, req, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("token must never be offered to github.com; got:\n%s", out.String())
	}
}

func TestRunCredentialNonGetIsNoop(t *testing.T) {
	wt := credentialFixture(t, "tok")
	for _, op := range []string{"store", "erase", ""} {
		var out, errOut strings.Builder
		req := strings.NewReader("protocol=https\nhost=gitea.example.com:3000\n\n")
		if err := runCredential(op, wt, req, &out, &errOut); err != nil {
			t.Fatalf("op %q: %v", op, err)
		}
		if out.Len() != 0 {
			t.Errorf("op %q should produce no output; got:\n%s", op, out.String())
		}
	}
}

func TestRunCredentialMissingTokenDiagnoses(t *testing.T) {
	wt := credentialFixture(t, "") // no token written
	var out, errOut strings.Builder
	req := strings.NewReader("protocol=https\nhost=gitea.example.com:3000\n\n")
	if err := runCredential("get", wt, req, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("no token → no stdout (git falls through); got:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "botfam credential") {
		t.Errorf("expected a stderr diagnostic when the matched host has no token; got: %q", errOut.String())
	}
}

func TestRunCredentialEnvOverride(t *testing.T) {
	// All three overrides set → answer without any fam.toml, from a bare dir.
	dir := t.TempDir()
	tf := filepath.Join(dir, "tok")
	if err := os.WriteFile(tf, []byte("envtoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOTFAM_FORGE_HOST", "localhost:13000")
	t.Setenv("BOTFAM_FORGE_USER", "carol-bot")
	t.Setenv("BOTFAM_TOKEN_FILE", tf)

	var out, errOut strings.Builder
	req := strings.NewReader("protocol=http\nhost=localhost:13000\n\n")
	if err := runCredential("get", dir, req, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "username=carol-bot\n") || !strings.Contains(got, "password=envtoken\n") {
		t.Errorf("env-override answer wrong; got:\n%s", got)
	}
}

func TestRunCredentialNonAgentWorktreeSilent(t *testing.T) {
	t.Setenv("BOTFAM_FORGE_HOST", "")
	t.Setenv("BOTFAM_FORGE_USER", "")
	t.Setenv("BOTFAM_TOKEN_FILE", "")
	// A bare temp dir with no fam.toml: ResolveFam fails → silent fall-through.
	dir := t.TempDir()
	var out, errOut strings.Builder
	req := strings.NewReader("protocol=https\nhost=gitea.example.com:3000\n\n")
	if err := runCredential("get", dir, req, &out, &errOut); err != nil {
		t.Fatalf("should not error outside an agent worktree: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("non-agent worktree should produce no output; got:\n%s", out.String())
	}
}
