package fam

import (
	"bytes"
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
