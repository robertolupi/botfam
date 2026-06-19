package metareview

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
)

// ForgeAPI is the subset of forge.Client the driver needs. An interface so the
// driver is unit-testable with a fake forge (no network).
type ForgeAPI interface {
	GetIssue(ctx context.Context, num int) (*forge.Issue, error)
	GetPRDiff(ctx context.Context, num int) (string, error)
	PostIssueComment(ctx context.Context, num int, body string) error
	ListRepoLabels(ctx context.Context) ([]forge.Label, error)
	AddLabels(ctx context.Context, num int, ids []int64) error
}

// Options configure one driver run over a single artifact.
type Options struct {
	Number   int
	Forge    ForgeAPI
	Corpus   Corpus     // tree + Lineage state to check against
	Local    Classifier // confirms+phrases the mechanical (80%) risks; required
	Escalate Classifier // confirms the judgment-heavy risks; optional (nil → local at low confidence)
	ModelTag string     // attribution string for the comment footer, e.g. "ollama:qwen2.5-coder seed=42"

	ApplyLabels bool // also apply high-confidence labels (additive); default comment-only
	DryRun      bool // print the comment instead of posting; no writes at all
	Out         io.Writer
}

// Result reports what a run produced (for the CLI summary and the eval harness).
type Result struct {
	Artifact    Artifact
	Suggestions []Suggestion
	Comment     string
	Posted      bool
	LabelsAdded []string
}

// Run executes the immediate meta-review over one artifact: fetch it, run the
// deterministic detectors, have the model(s) confirm+phrase, then post a single
// advisory comment (and optionally apply high-confidence labels). Read-only
// except for that one comment and the optional additive labels.
func Run(ctx context.Context, opts Options) (*Result, error) {
	art, err := fetchArtifact(ctx, opts.Forge, opts.Number)
	if err != nil {
		return nil, err
	}

	suggestions, err := Assess(ctx, art, opts.Corpus, opts.Local, opts.Escalate, opts.Out)
	if err != nil {
		return nil, err
	}
	comment := ComposeComment(suggestions, opts.ModelTag)
	res := &Result{Artifact: art, Suggestions: suggestions, Comment: comment}

	if opts.DryRun {
		fmt.Fprintf(opts.Out, "%s\n", comment)
		return res, nil
	}

	if err := opts.Forge.PostIssueComment(ctx, opts.Number, comment); err != nil {
		return nil, fmt.Errorf("post comment: %w", err)
	}
	res.Posted = true
	fmt.Fprintf(opts.Out, "posted advisory comment on #%d (%d suggestion(s))\n", opts.Number, len(suggestions))

	if opts.ApplyLabels {
		added, err := applyHighConfidence(ctx, opts.Forge, opts.Number, suggestions)
		if err != nil {
			fmt.Fprintf(opts.Out, "  warning: applying labels failed: %v\n", err)
		}
		res.LabelsAdded = added
		if len(added) > 0 {
			fmt.Fprintf(opts.Out, "  applied label(s): %s\n", strings.Join(added, ", "))
		}
	}
	return res, nil
}

// Assess runs the deterministic detectors and the model confirmation over one
// artifact, returning the validated+deduped suggestions. It is the shared core
// of Run (which posts them) and the eval harness (which scores them). out
// receives non-fatal warnings; pass io.Discard to silence.
//
// Tiering (botfam#306): mechanical risks go to the local model keeping its
// confidence; judgment-heavy risks go to the escalation model when configured,
// otherwise to the local model with confidence clamped to low — so
// hollow-validation is never silently trusted.
func Assess(ctx context.Context, art Artifact, corpus Corpus, local, escalate Classifier, out io.Writer) ([]Suggestion, error) {
	if out == nil {
		out = io.Discard
	}
	cands := Detect(art, corpus)
	mechanical, judgment := partition(cands)

	var suggestions []Suggestion
	if len(mechanical) > 0 {
		s, err := confirm(ctx, local, art, mechanical)
		if err != nil {
			return nil, fmt.Errorf("local classify: %w", err)
		}
		suggestions = append(suggestions, s...)
	}
	clf, clamp := escalate, false
	if clf == nil {
		// No escalation model: run the judgment risks on the local model at low
		// confidence, but drop the EscalateOnly ones (hollow-validation) — the
		// local model can't tell them from ordinary literal assertions, so
		// without a strong model they are noise, not signal (PR botfam#328
		// dogfood). speculative (cue-based) still runs locally.
		clf, clamp = local, true
		judgment = dropEscalateOnly(judgment)
	}
	if len(judgment) > 0 {
		s, err := confirm(ctx, clf, art, judgment)
		if err != nil {
			// A judgment-model failure must not block the mechanical findings;
			// drop the judgment candidates with a warning (safer than trusting).
			fmt.Fprintf(out, "  warning: escalation classify failed, dropping %d judgment candidate(s): %v\n", len(judgment), err)
		} else {
			if clamp {
				for i := range s {
					s[i].Confidence = "low"
				}
			}
			suggestions = append(suggestions, s...)
		}
	}
	return dedupe(ValidateSuggestions(suggestions)), nil
}

// fetchArtifact loads the issue/PR and (for PRs) its diff.
func fetchArtifact(ctx context.Context, api ForgeAPI, num int) (Artifact, error) {
	iss, err := api.GetIssue(ctx, num)
	if err != nil {
		return Artifact{}, fmt.Errorf("get issue #%d: %w", num, err)
	}
	art := Artifact{Number: num, Kind: "issue", Title: iss.Title, Body: iss.Body}
	if iss.PullRequest != nil {
		art.Kind = "pr"
		if diff, err := api.GetPRDiff(ctx, num); err == nil {
			art.Diff = diff
		}
	}
	return art, nil
}

// partition splits candidates into mechanical (local model) and judgment-heavy
// (escalation) sets, by the Escalate flag the detectors set.
func partition(cands []Candidate) (mechanical, judgment []Candidate) {
	for _, c := range cands {
		if c.Escalate {
			judgment = append(judgment, c)
		} else {
			mechanical = append(mechanical, c)
		}
	}
	return mechanical, judgment
}

// dropEscalateOnly removes candidates that must not be surfaced without an
// escalation model (hollow-validation).
func dropEscalateOnly(cands []Candidate) []Candidate {
	out := cands[:0:0]
	for _, c := range cands {
		if !c.EscalateOnly {
			out = append(out, c)
		}
	}
	return out
}

// confirm builds the prompt for a candidate set and runs the classifier,
// returning the validated suggestions.
func confirm(ctx context.Context, clf Classifier, art Artifact, cands []Candidate) ([]Suggestion, error) {
	prompt := BuildPrompt(art, cands)
	raw, err := clf.Classify(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return ValidateSuggestions(raw), nil
}

// dedupe removes exact (label, evidence) duplicates, preferring the higher
// confidence, and returns a stable order (label, then evidence).
func dedupe(in []Suggestion) []Suggestion {
	best := map[string]Suggestion{}
	for _, s := range in {
		key := s.Label + "\x00" + s.Evidence
		if prev, ok := best[key]; !ok || (prev.Confidence != "high" && s.Confidence == "high") {
			best[key] = s
		}
	}
	out := make([]Suggestion, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].Evidence < out[j].Evidence
	})
	return out
}

// applyHighConfidence applies the labels of high-confidence suggestions
// (additive). Low-confidence suggestions are comment-only — the author confirms
// (Decide-not-Consensus). Never applies a triage/* disposition that folds an
// item; only the risk/* label.
func applyHighConfidence(ctx context.Context, api ForgeAPI, num int, suggestions []Suggestion) ([]string, error) {
	want := map[string]bool{}
	for _, s := range suggestions {
		if s.Confidence == "high" {
			want[s.Label] = true
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	labels, err := api.ListRepoLabels(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	var names []string
	for _, l := range labels {
		if want[l.Name] {
			ids = append(ids, l.ID)
			names = append(names, l.Name)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := api.AddLabels(ctx, num, ids); err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
