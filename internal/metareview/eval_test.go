package metareview

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// candidateEchoClassifier confirms exactly the candidates the driver passed in
// (parsed from the prompt's numbered candidate list). It is a faithful "perfect
// model" oracle: it confirms every deterministic candidate, so the eval then
// measures the DETECTOR's precision/recall — which is what we validate offline,
// without a live model.
type candidateEchoClassifier struct{}

var promptCandidateRe = regexp.MustCompile(`(?m)^\s*\d+\.\s+(risk/[a-z-]+)\s+—`)

func (candidateEchoClassifier) Classify(ctx context.Context, prompt string) ([]Suggestion, error) {
	var out []Suggestion
	for _, m := range promptCandidateRe.FindAllStringSubmatch(prompt, -1) {
		out = append(out, Suggestion{Label: m[1], Evidence: "confirmed " + m[1], Confidence: "high"})
	}
	return out, nil
}

func TestLoadEvalCases_BothShapes(t *testing.T) {
	arr := []byte(`[{"name":"a","kind":"issue","expected":["risk/superseded"]}]`)
	cs, err := LoadEvalCases(arr)
	if err != nil || len(cs) != 1 {
		t.Fatalf("array form: %v %+v", err, cs)
	}
	obj := []byte(`{"cases":[{"name":"a","kind":"issue","expected":[]},{"name":"b","kind":"pr","expected":[]}]}`)
	cs, err = LoadEvalCases(obj)
	if err != nil || len(cs) != 2 {
		t.Fatalf("object form: %v %+v", err, cs)
	}
}

func TestEval_PerLabelMetrics(t *testing.T) {
	cases := []EvalCase{
		{
			// True positive for phase-inversion: references an absent file, and it's expected.
			Name:     "tp-phase",
			Kind:     "issue",
			Body:     "depends on `internal/trace/schema.go`",
			Tree:     nil,
			Expected: []string{LabelPhaseInversion},
		},
		{
			// False positive for phase-inversion: detector fires but it's not expected.
			Name:     "fp-phase",
			Kind:     "issue",
			Body:     "depends on `internal/gone/x.go`",
			Tree:     nil,
			Expected: []string{},
		},
		{
			// False negative for superseded: expected but the detector cannot see it
			// (no Lineage supplied, so nothing fires).
			Name:     "fn-superseded",
			Kind:     "issue",
			Body:     "builds on something",
			Expected: []string{LabelSuperseded},
		},
	}
	rep, err := Eval(context.Background(), cases, candidateEchoClassifier{}, nil, io.Discard)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if rep.Cases != 3 {
		t.Errorf("cases: %d", rep.Cases)
	}
	byLabel := map[string]LabelScore{}
	for _, s := range rep.Scores {
		byLabel[s.Label] = s
	}
	phase := byLabel[LabelPhaseInversion]
	if phase.TP != 1 || phase.FP != 1 {
		t.Errorf("phase-inversion: want TP=1 FP=1, got TP=%d FP=%d", phase.TP, phase.FP)
	}
	if phase.Precision != 0.5 {
		t.Errorf("phase-inversion precision: want 0.5, got %v", phase.Precision)
	}
	sup := byLabel[LabelSuperseded]
	if sup.FN != 1 || sup.Recall != 0 {
		t.Errorf("superseded: want FN=1 recall=0, got FN=%d recall=%v", sup.FN, sup.Recall)
	}
}

// TestEval_ShippedSet runs the in-repo labelled set (the deliverable from
// botfam#306's acceptance criteria) through the detectors with the perfect-
// model oracle, asserting the detectors recover every confirmed label with no
// false positives — i.e. the shipped set is well-formed and the deterministic
// retrieval is sound on it.
func TestEval_ShippedSet(t *testing.T) {
	path := filepath.Join("..", "..", "doc", "review", "metareview-eval-set.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shipped eval set: %v", err)
	}
	cases, err := LoadEvalCases(data)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cases) < 4 {
		t.Fatalf("expected a non-trivial set, got %d cases", len(cases))
	}
	rep, err := Eval(context.Background(), cases, candidateEchoClassifier{}, nil, io.Discard)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	for _, s := range rep.Scores {
		if s.FP != 0 {
			t.Errorf("%s: detectors produced %d false positive(s) on the shipped set", s.Label, s.FP)
		}
		if s.TP+s.FN > 0 && s.Recall != 1 {
			t.Errorf("%s: detectors missed an expected label (recall=%.2f, FN=%d)", s.Label, s.Recall, s.FN)
		}
	}
	t.Logf("shipped-set detector scores:\n%s", FormatReport(rep))
}
