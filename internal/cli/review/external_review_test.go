package review

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
)

// zeroReviewOpts builds options that pass the early model/material gates but
// produce no reviews: an OpenAI model is selected while OPENAI_API_KEY is unset,
// so the provider is skipped and `ran` stays empty.
func zeroReviewOpts(t *testing.T) externalReviewOpts {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")

	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("review this.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(dir, "session.md")
	if err := os.WriteFile(sessionFile, []byte("# session\nstuff happened\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return externalReviewOpts{
		promptFile:  promptFile,
		outDir:      filepath.Join(dir, "out"),
		sessionFile: sessionFile,
		openaiM:     []string{"gpt-4o"},
	}
}

func TestExternalReviewFailsClosedOnZeroReviews(t *testing.T) {
	opts := zeroReviewOpts(t)

	var out bytes.Buffer
	err := runExternalReview(context.Background(), opts, &out)
	if err == nil {
		t.Fatalf("expected an error when zero reviews are produced, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "no model reviews") {
		t.Errorf("expected a 'no model reviews' error, got: %v", err)
	}
}

func TestExternalReviewAllowZeroReviews(t *testing.T) {
	opts := zeroReviewOpts(t)
	opts.allowZeroReviews = true

	var out bytes.Buffer
	if err := runExternalReview(context.Background(), opts, &out); err != nil {
		t.Fatalf("--allow-zero-reviews should succeed with zero reviews, got: %v", err)
	}
	// Supporting artifacts must still be written.
	for _, name := range []string{"session.md", "combined-prompt.txt", "MANIFEST.txt"} {
		if _, err := os.Stat(filepath.Join(opts.outDir, name)); err != nil {
			t.Errorf("expected artifact %s to exist: %v", name, err)
		}
	}
}

// chatCompletionStub is a minimal OpenAI-compatible /v1/chat/completions server
// that echoes a per-request review body, used to drive runExternalReview end to
// end without real providers.
func chatCompletionStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"` + body.Model +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"review by ` + body.Model + `"},"finish_reason":"stop"}]}`))
	}))
}

func TestExternalReviewWritesAllModelReviews(t *testing.T) {
	server := chatCompletionStub(t)
	defer server.Close()

	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("review this.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(dir, "session.md")
	if err := os.WriteFile(sessionFile, []byte("# session\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")

	opts := externalReviewOpts{
		promptFile:  promptFile,
		outDir:      outDir,
		sessionFile: sessionFile,
		ollamaHost:  server.URL,
		ollama:      []string{"m1", "m2", "m3"},
	}

	var out bytes.Buffer
	if err := runExternalReview(context.Background(), opts, &out); err != nil {
		t.Fatalf("runExternalReview failed: %v\noutput:\n%s", err, out.String())
	}

	// Every selected model must have produced a review file with its content.
	for _, m := range []string{"m1", "m2", "m3"} {
		p := filepath.Join(outDir, "review-ollama-"+m+".md")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("expected review file %s: %v", p, err)
			continue
		}
		if want := "review by " + m; string(b) != want {
			t.Errorf("review %s = %q, want %q", p, string(b), want)
		}
	}

	// MANIFEST must list all three models.
	man, err := os.ReadFile(filepath.Join(outDir, "MANIFEST.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"ollama:m1", "ollama:m2", "ollama:m3"} {
		if !strings.Contains(string(man), m) {
			t.Errorf("MANIFEST missing %s:\n%s", m, string(man))
		}
	}
}

func TestLoadSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	content := "# a comment\n\nOPENAI_API_KEY=sk-plain\nGEMINI_API_KEY=\"sk-quoted\"\n  ANTHROPIC_API_KEY = sk-spaced \nNOEQUALS\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"OPENAI_API_KEY":    "sk-plain",
		"GEMINI_API_KEY":    "sk-quoted",
		"ANTHROPIC_API_KEY": "sk-spaced",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("loadSecrets[%s] = %q, want %q", k, m[k], v)
		}
	}
	if _, ok := m["NOEQUALS"]; ok {
		t.Errorf("line without '=' should be skipped, got %q", m["NOEQUALS"])
	}
	if len(m) != 3 {
		t.Errorf("expected 3 keys, got %d: %v", len(m), m)
	}
}

// TestMergeSecrets verifies the #438 precedence: a --secrets file overrides the
// config [secrets] stanza, empty config values are dropped, and config-only keys
// survive. (Environment fallback is applied later by lookupKey, not here.)
func TestMergeSecrets(t *testing.T) {
	cfg := map[string]string{
		"GEMINI_API_KEY":    "from-config",
		"OPENAI_API_KEY":    "config-openai",
		"ANTHROPIC_API_KEY": "", // empty → dropped
	}

	// No file: config keys come through; empty ones are dropped.
	m, err := mergeSecrets(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if m["GEMINI_API_KEY"] != "from-config" || m["OPENAI_API_KEY"] != "config-openai" {
		t.Errorf("config keys not carried: %v", m)
	}
	if _, ok := m["ANTHROPIC_API_KEY"]; ok {
		t.Errorf("empty config value should be dropped, got %q", m["ANTHROPIC_API_KEY"])
	}

	// A --secrets file overrides the matching config key, adds new ones.
	dir := t.TempDir()
	path := filepath.Join(dir, "s.env")
	if err := os.WriteFile(path, []byte("GEMINI_API_KEY=from-file\nNEW_KEY=n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = mergeSecrets(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if m["GEMINI_API_KEY"] != "from-file" {
		t.Errorf("file should override config: got %q, want from-file", m["GEMINI_API_KEY"])
	}
	if m["OPENAI_API_KEY"] != "config-openai" {
		t.Errorf("config-only key should survive: %v", m)
	}
	if m["NEW_KEY"] != "n" {
		t.Errorf("file-only key missing: %v", m)
	}
}

// TestExternalReviewWikiAndLMStudio drives the LM Studio provider and the
// --wiki material source end to end against the chat-completions stub.
func TestExternalReviewWikiAndLMStudio(t *testing.T) {
	server := chatCompletionStub(t)
	defer server.Close()

	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("critique this.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wikiDir := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wikiDir, "MyPage.md"), []byte("# MyPage\nthe design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")

	opts := externalReviewOpts{
		promptFile:   promptFile,
		outDir:       outDir,
		lmstudioHost: server.URL,
		lmstudio:     []string{"local-m"},
		wiki:         []string{"MyPage"}, // also exercises the .md-suffix-stripping path below
		wikiDir:      wikiDir,
	}

	var out bytes.Buffer
	if err := runExternalReview(context.Background(), opts, &out); err != nil {
		t.Fatalf("runExternalReview failed: %v\noutput:\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "review-lmstudio-local-m.md")); err != nil {
		t.Errorf("expected lmstudio review file: %v", err)
	}
	combined, err := os.ReadFile(filepath.Join(outDir, "combined-prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(combined), "the design") {
		t.Errorf("combined prompt should include the wiki page content:\n%s", string(combined))
	}
}

func TestExternalReviewWikiPageNotFound(t *testing.T) {
	dir := t.TempDir()
	opts := externalReviewOpts{
		outDir:   filepath.Join(dir, "out"),
		wikiDir:  dir, // empty, page absent
		wiki:     []string{"Missing"},
		lmstudio: []string{"m"},
	}
	err := runExternalReview(context.Background(), opts, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "wiki page") {
		t.Fatalf("expected a 'wiki page ... not found' error, got: %v", err)
	}
}

// TestExternalReviewDesignFlag verifies --design selects the design prompt path
// (here surfaced as a not-found error citing that path, which proves the wiring
// without depending on the repo file being reachable from the test cwd).
func TestExternalReviewDesignFlag(t *testing.T) {
	dir := t.TempDir()
	mat := filepath.Join(dir, "m.md")
	if err := os.WriteFile(mat, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := cmdutil.RunCobra(NewExternalReviewCmd(), []string{"--design", "--lmstudio", "m", "--out", filepath.Join(dir, "o"), mat}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), defaultDesignPrompt) {
		t.Fatalf("expected --design to select %s, got err: %v", defaultDesignPrompt, err)
	}
}
