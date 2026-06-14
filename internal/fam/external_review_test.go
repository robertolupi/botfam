package fam

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	err := runExternalReview(opts, &out)
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
	if err := runExternalReview(opts, &out); err != nil {
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
	if err := runExternalReview(opts, &out); err != nil {
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
