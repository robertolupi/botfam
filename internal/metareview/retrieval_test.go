package metareview

import (
	"strings"
	"testing"
)

// fakeCorpus is an in-memory Corpus for detection tests.
type fakeCorpus struct {
	files   map[string]bool
	lineage string
}

func (f fakeCorpus) FileExists(p string) bool { return f.files[p] }
func (f fakeCorpus) Lineage() string          { return f.lineage }

func TestReferencedPaths(t *testing.T) {
	body := "We open `internal/trace/schema.go` and read (doc/spec.md). See http://x/y/z.go and `internal/trace/`."
	got := referencedPaths(body)
	want := map[string]bool{
		"internal/trace/schema.go": true,
		"doc/spec.md":              true,
		"internal/trace":           true, // trailing slash trimmed
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want keys %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected path %q (URLs must be excluded)", p)
		}
	}
}

func TestDetectPhaseInversion(t *testing.T) {
	art := Artifact{
		Kind: "issue",
		Body: "Depends on `internal/trace/schema.go` and uses `internal/cli/root.go`.",
	}
	corpus := fakeCorpus{files: map[string]bool{"internal/cli/root.go": true}}
	cands := detectPhaseInversion(art, corpus)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(cands), cands)
	}
	if cands[0].Label != LabelPhaseInversion {
		t.Errorf("label: %s", cands[0].Label)
	}
	if !strings.Contains(cands[0].Evidence, "internal/trace/schema.go") {
		t.Errorf("evidence missing path: %s", cands[0].Evidence)
	}
	if cands[0].Triage != "triage/blocked" {
		t.Errorf("triage: %s", cands[0].Triage)
	}
}

func TestDetectPhaseInversion_DiffAddedFileNotFlagged(t *testing.T) {
	art := Artifact{
		Kind: "pr",
		Body: "Adds `internal/trace/schema.go`.",
		Diff: "+++ b/internal/trace/schema.go\n+package trace\n",
	}
	corpus := fakeCorpus{files: map[string]bool{}}
	if cands := detectPhaseInversion(art, corpus); len(cands) != 0 {
		t.Errorf("a file the PR creates must not be flagged: %+v", cands)
	}
}

func TestDetectSuperseded(t *testing.T) {
	art := Artifact{Body: "We extend the design in [ccrep](lineage-botfam-ccrep.md)."}
	corpus := fakeCorpus{lineage: "| CCREP | [lineage-botfam-ccrep](lineage-botfam-ccrep.md) — superseded; consensus is now branch protection |"}
	cands := detectSuperseded(art, corpus)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d: %+v", len(cands), cands)
	}
	if cands[0].Label != LabelSuperseded || !strings.Contains(cands[0].Evidence, "lineage-botfam-ccrep") {
		t.Errorf("bad candidate: %+v", cands[0])
	}
}

func TestDetectSuperseded_NotSupersededNotFlagged(t *testing.T) {
	art := Artifact{Body: "Builds on [arch](Architecture.md)."}
	corpus := fakeCorpus{lineage: "| arch | live page, governs current system |"}
	if cands := detectSuperseded(art, corpus); len(cands) != 0 {
		t.Errorf("a non-superseded decision must not be flagged: %+v", cands)
	}
}

func TestDetectHollowValidation_EscalatesLowConfidence(t *testing.T) {
	art := Artifact{
		Kind: "pr",
		Diff: "+++ b/internal/foo/foo_test.go\n+\tif got != \"expected\" {\n+\t\trequire.Equal(t, 42, got)\n",
	}
	cands := detectHollowValidation(art)
	if len(cands) == 0 {
		t.Fatal("expected hollow-validation candidate from test assertion")
	}
	for _, c := range cands {
		if c.Label != LabelHollowValidation {
			t.Errorf("label: %s", c.Label)
		}
		if !c.Escalate {
			t.Error("hollow-validation candidates must be marked Escalate (never silently trusted)")
		}
	}
}

func TestDetectHollowValidation_NonTestFileIgnored(t *testing.T) {
	art := Artifact{Kind: "pr", Diff: "+++ b/internal/foo/foo.go\n+\trequire.Equal(t, 42, got)\n"}
	if cands := detectHollowValidation(art); len(cands) != 0 {
		t.Errorf("non-test file must not yield hollow-validation: %+v", cands)
	}
}

func TestDetectSpeculative(t *testing.T) {
	art := Artifact{Body: "We build the ranker before we have any labelled data."}
	cands := detectSpeculative(art)
	if len(cands) != 1 || !cands[0].Escalate {
		t.Fatalf("want 1 escalating speculative candidate, got %+v", cands)
	}
}

func TestDetect_CleanArtifact(t *testing.T) {
	art := Artifact{Kind: "issue", Body: "A tidy change using `internal/cli/root.go`."}
	corpus := fakeCorpus{files: map[string]bool{"internal/cli/root.go": true}}
	if cands := Detect(art, corpus); len(cands) != 0 {
		t.Errorf("clean artifact should yield no candidates, got %+v", cands)
	}
}
