package metareview

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
)

// fakeForge is an in-memory ForgeAPI.
type fakeForge struct {
	issue       *forge.Issue
	diff        string
	labels      []forge.Label
	postedBody  string
	addedIDs    []int64
	postErr     error
	addLabelErr error
}

func (f *fakeForge) GetIssue(num int) (*forge.Issue, error) { return f.issue, nil }
func (f *fakeForge) GetPRDiff(num int) (string, error)      { return f.diff, nil }
func (f *fakeForge) PostIssueComment(num int, body string) error {
	if f.postErr != nil {
		return f.postErr
	}
	f.postedBody = body
	return nil
}
func (f *fakeForge) ListRepoLabels() ([]forge.Label, error) { return f.labels, nil }
func (f *fakeForge) AddLabels(num int, ids []int64) error {
	if f.addLabelErr != nil {
		return f.addLabelErr
	}
	f.addedIDs = append(f.addedIDs, ids...)
	return nil
}

// fakeClassifier returns canned suggestions and records the prompt it saw, so
// tests are fully deterministic (the reproducibility property holds trivially).
type fakeClassifier struct {
	ret       []Suggestion
	err       error
	gotPrompt string
}

func (c *fakeClassifier) Classify(ctx context.Context, prompt string) ([]Suggestion, error) {
	c.gotPrompt = prompt
	return c.ret, c.err
}

func phaseInversionIssue() *forge.Issue {
	return &forge.Issue{Number: 42, Title: "x", Body: "Depends on `internal/trace/schema.go`."}
}

func TestRun_PostsAdvisoryComment(t *testing.T) {
	f := &fakeForge{issue: phaseInversionIssue()}
	local := &fakeClassifier{ret: []Suggestion{
		{Label: LabelPhaseInversion, Evidence: "references `internal/trace/schema.go`, absent", Confidence: "high"},
	}}
	var out bytes.Buffer
	res, err := Run(context.Background(), Options{
		Number: 42, Forge: f, Corpus: fakeCorpus{files: map[string]bool{}},
		Local: local, ModelTag: "ollama:test seed=1", Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Posted || f.postedBody == "" {
		t.Fatal("expected a posted comment")
	}
	if !strings.Contains(f.postedBody, "risk/phase-inversion") || !strings.Contains(f.postedBody, "triage/blocked") {
		t.Errorf("comment missing finding/triage:\n%s", f.postedBody)
	}
	if !strings.Contains(f.postedBody, "ollama:test seed=1") {
		t.Errorf("comment missing model attribution:\n%s", f.postedBody)
	}
}

func TestRun_CleanArtifactSkipsModel(t *testing.T) {
	f := &fakeForge{issue: &forge.Issue{Number: 7, Title: "clean", Body: "Uses `internal/cli/root.go`."}}
	local := &fakeClassifier{ret: []Suggestion{{Label: LabelPhaseInversion, Evidence: "should not be asked", Confidence: "high"}}}
	var out bytes.Buffer
	_, err := Run(context.Background(), Options{
		Number: 7, Forge: f, Corpus: fakeCorpus{files: map[string]bool{"internal/cli/root.go": true}},
		Local: local, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if local.gotPrompt != "" {
		t.Error("clean artifact must not invoke the model")
	}
	if !strings.Contains(f.postedBody, "No per-artifact risks found.") {
		t.Errorf("expected clean line, got:\n%s", f.postedBody)
	}
}

func TestRun_HollowValidationClampedLowWithoutEscalation(t *testing.T) {
	iss := &forge.Issue{Number: 9, Title: "pr", Body: "test change", PullRequest: &struct {
		URL string `json:"url"`
	}{URL: "u"}}
	f := &fakeForge{
		issue: iss,
		diff:  "+++ b/internal/foo/foo_test.go\n+\trequire.Equal(t, 42, got)\n",
	}
	// Local model "confirms" at high confidence; the driver must clamp it to low
	// because no escalation model is configured.
	local := &fakeClassifier{ret: []Suggestion{
		{Label: LabelHollowValidation, Evidence: "foo_test.go re-asserts 42", Confidence: "high"},
	}}
	var out bytes.Buffer
	res, err := Run(context.Background(), Options{
		Number: 9, Forge: f, Corpus: fakeCorpus{}, Local: local, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Suggestions) != 1 || res.Suggestions[0].Confidence != "low" {
		t.Fatalf("hollow-validation must be clamped to low confidence without escalation: %+v", res.Suggestions)
	}
}

func TestRun_DryRunDoesNotPost(t *testing.T) {
	f := &fakeForge{issue: phaseInversionIssue()}
	local := &fakeClassifier{ret: []Suggestion{{Label: LabelPhaseInversion, Evidence: "references `internal/trace/schema.go`", Confidence: "high"}}}
	var out bytes.Buffer
	res, err := Run(context.Background(), Options{
		Number: 42, Forge: f, Corpus: fakeCorpus{}, Local: local, DryRun: true, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Posted || f.postedBody != "" {
		t.Error("dry run must not post")
	}
	if !strings.Contains(out.String(), "risk/phase-inversion") {
		t.Errorf("dry run should print the comment, got:\n%s", out.String())
	}
}

func TestRun_AppliesOnlyHighConfidenceLabels(t *testing.T) {
	f := &fakeForge{
		issue:  phaseInversionIssue(),
		labels: []forge.Label{{ID: 1, Name: LabelPhaseInversion}, {ID: 2, Name: LabelSpeculative}},
	}
	local := &fakeClassifier{ret: []Suggestion{
		{Label: LabelPhaseInversion, Evidence: "references `internal/trace/schema.go`", Confidence: "high"},
		{Label: LabelSpeculative, Evidence: "weak foundation", Confidence: "low"},
	}}
	var out bytes.Buffer
	res, err := Run(context.Background(), Options{
		Number: 42, Forge: f, Corpus: fakeCorpus{}, Local: local, ApplyLabels: true, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(f.addedIDs) != 1 || f.addedIDs[0] != 1 {
		t.Errorf("only the high-confidence label (id 1) should be applied, got %v", f.addedIDs)
	}
	if len(res.LabelsAdded) != 1 || res.LabelsAdded[0] != LabelPhaseInversion {
		t.Errorf("LabelsAdded: %v", res.LabelsAdded)
	}
}

func TestRun_MalformedModelOutputDropped(t *testing.T) {
	f := &fakeForge{issue: phaseInversionIssue()}
	// Model emits an unknown label and a blank-evidence entry — both dropped.
	local := &fakeClassifier{ret: []Suggestion{
		{Label: "risk/bogus", Evidence: "x", Confidence: "high"},
		{Label: LabelPhaseInversion, Evidence: "  ", Confidence: "high"},
	}}
	var out bytes.Buffer
	res, err := Run(context.Background(), Options{
		Number: 42, Forge: f, Corpus: fakeCorpus{}, Local: local, Out: &out,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Suggestions) != 0 {
		t.Errorf("malformed suggestions must be dropped, got %+v", res.Suggestions)
	}
	if !strings.Contains(f.postedBody, "No per-artifact risks found.") {
		t.Errorf("expected clean line after dropping malformed output, got:\n%s", f.postedBody)
	}
}

func TestRun_LocalClassifyErrorFails(t *testing.T) {
	f := &fakeForge{issue: phaseInversionIssue()}
	local := &fakeClassifier{err: errors.New("model down")}
	var out bytes.Buffer
	_, err := Run(context.Background(), Options{Number: 42, Forge: f, Corpus: fakeCorpus{}, Local: local, Out: &out})
	if err == nil {
		t.Fatal("expected error when local classifier fails")
	}
	if f.postedBody != "" {
		t.Error("must not post when classification failed")
	}
}
