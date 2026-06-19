package metareview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// EvalCase is one self-contained labelled artifact: it carries its own corpus
// (the tree file list + Lineage text) so the eval is reproducible offline, with
// no forge or checkout. Expected is the set of risk labels a human confirmed.
type EvalCase struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"` // "issue" | "pr"
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Diff     string   `json:"diff,omitempty"`
	Tree     []string `json:"tree,omitempty"`
	Lineage  string   `json:"lineage,omitempty"`
	Expected []string `json:"expected"`
}

// corpus builds a Corpus from the case's self-contained state.
func (c EvalCase) corpus() Corpus {
	files := make(map[string]bool, len(c.Tree))
	for _, f := range c.Tree {
		files[f] = true
	}
	return fixedCorpus{files: files, lineage: c.Lineage}
}

type fixedCorpus struct {
	files   map[string]bool
	lineage string
}

func (c fixedCorpus) FileExists(p string) bool { return c.files[p] }
func (c fixedCorpus) Lineage() string          { return c.lineage }

// LabelScore is per-label precision/recall over the eval set.
type LabelScore struct {
	Label     string  `json:"label"`
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

// EvalReport is the full per-risk result plus the cases evaluated.
type EvalReport struct {
	Cases  int          `json:"cases"`
	Scores []LabelScore `json:"scores"`
}

// LoadEvalCases parses a labelled set: either a bare JSON array of cases or an
// object {"cases":[...]}.
func LoadEvalCases(data []byte) ([]EvalCase, error) {
	var arr []EvalCase
	if err := json.Unmarshal(data, &arr); err == nil {
		return arr, nil
	}
	var wrapper struct {
		Cases []EvalCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parse eval set: %w", err)
	}
	return wrapper.Cases, nil
}

// Eval scores the driver's suggestions against the labelled set, per risk
// label. It compares the set of distinct predicted labels per case to the
// expected set (label-level, not row-level: multiple evidence rows for one
// label count once). out receives per-case warnings.
func Eval(ctx context.Context, cases []EvalCase, local, escalate Classifier, out io.Writer) (*EvalReport, error) {
	if out == nil {
		out = io.Discard
	}
	type counts struct{ tp, fp, fn int }
	tally := map[string]*counts{}
	for _, l := range PerArtifactLabels {
		tally[l] = &counts{}
	}

	for _, c := range cases {
		art := Artifact{Kind: c.Kind, Title: c.Title, Body: c.Body, Diff: c.Diff}
		suggestions, err := Assess(ctx, art, c.corpus(), local, escalate, out)
		if err != nil {
			return nil, fmt.Errorf("case %q: %w", c.Name, err)
		}
		predicted := labelSet(suggestionLabels(suggestions))
		expected := labelSet(c.Expected)
		for _, l := range PerArtifactLabels {
			switch {
			case predicted[l] && expected[l]:
				tally[l].tp++
			case predicted[l] && !expected[l]:
				tally[l].fp++
			case !predicted[l] && expected[l]:
				tally[l].fn++
			}
		}
	}

	rep := &EvalReport{Cases: len(cases)}
	for _, l := range PerArtifactLabels {
		t := tally[l]
		rep.Scores = append(rep.Scores, LabelScore{
			Label:     l,
			TP:        t.tp,
			FP:        t.fp,
			FN:        t.fn,
			Precision: ratio(t.tp, t.tp+t.fp),
			Recall:    ratio(t.tp, t.tp+t.fn),
			F1:        f1(ratio(t.tp, t.tp+t.fp), ratio(t.tp, t.tp+t.fn)),
		})
	}
	return rep, nil
}

func suggestionLabels(s []Suggestion) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Label
	}
	return out
}

func labelSet(labels []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range labels {
		m[l] = true
	}
	return m
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func f1(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// FormatReport renders the eval report as an aligned text table.
func FormatReport(rep *EvalReport) string {
	var b []byte
	b = append(b, fmt.Sprintf("eval over %d case(s)\n\n", rep.Cases)...)
	b = append(b, fmt.Sprintf("%-26s %4s %4s %4s  %9s %9s %9s\n", "label", "TP", "FP", "FN", "precision", "recall", "f1")...)
	scores := append([]LabelScore(nil), rep.Scores...)
	sort.Slice(scores, func(i, j int) bool { return scores[i].Label < scores[j].Label })
	for _, s := range scores {
		b = append(b, fmt.Sprintf("%-26s %4d %4d %4d  %9.2f %9.2f %9.2f\n",
			s.Label, s.TP, s.FP, s.FN, s.Precision, s.Recall, s.F1)...)
	}
	return string(b)
}
