// Package metareview implements the immediate (per-artifact) process-risk
// meta-review tier as a read-only driver: deterministic retrieval gathers
// candidate signals, a local model confirms and phrases them, and the driver
// posts a single advisory comment. It is the harness-agnostic, ollama-backed
// realization of the meta-review skill (botfam#306, reshaping botfam#300).
//
// The split is deliberate (botfam#306): the driver does the mechanical
// detection (grep referenced artifacts vs. the tree, look decisions up in
// Lineage); the model only classifies (is this candidate a real risk?) and
// writes the one-line evidence. Advisory output → a wrong suggestion is cheap,
// so a small local model suffices and code never leaves the box.
package metareview

// The per-artifact risk labels. These are the ONLY risk labels the immediate
// tier may emit; cross-artifact risks (risk/fragmentation, source/agent-burst)
// belong to the periodic batch tier (botfam#301) and are never produced here.
const (
	LabelPhaseInversion   = "risk/phase-inversion"
	LabelSuperseded       = "risk/superseded"
	LabelHollowValidation = "risk/hollow-validation"
	LabelSpeculative      = "risk/speculative"
)

// PerArtifactLabels is the closed set the model's output is validated against;
// any suggested label outside it is dropped.
var PerArtifactLabels = []string{
	LabelPhaseInversion,
	LabelSuperseded,
	LabelHollowValidation,
	LabelSpeculative,
}

// suggestedTriage maps a risk label to its obvious triage/* disposition (skill
// section "Behavior"). A blank means "no obvious disposition — let the author
// decide". Never a disposition that closes/folds an item; that is the author's
// call.
var suggestedTriage = map[string]string{
	LabelPhaseInversion: "triage/blocked",
	LabelSuperseded:     "triage/needs-sequencing",
}

// isPerArtifactLabel reports whether label is in the closed emit set.
func isPerArtifactLabel(label string) bool {
	for _, l := range PerArtifactLabels {
		if l == label {
			return true
		}
	}
	return false
}

// Glossary is the canonical label → detection-signal → evidence definition,
// embedded so the model classifies against the same definitions the skill
// documents (skill section "Scope"). Kept terse: the model only needs the
// discriminating rule and what counts as evidence.
const Glossary = `Process-risk glossary (per-artifact tier). Emit ONLY these labels:

- risk/phase-inversion — the artifact depends on an artifact (file, command,
  table, store) that is ABSENT from the tree and from merged PRs. Evidence: the
  missing artifact (name + where it was expected).
- risk/superseded — the artifact builds on a design decision flagged superseded
  in the Lineage page. Evidence: the superseding decision (the Lineage entry).
- risk/hollow-validation — a test or claim asserts what the code was written to
  produce, not an independent property. Evidence: the self-fulfilling assertion
  (file:line or quoted claim). HARD — judge carefully; if unsure, drop it.
- risk/speculative — the least-grounded component is being built before the
  evidence that would justify it exists. Evidence: what evidence is missing.

Rules: a suggestion needs a SPECIFIC cited artifact — vague unease is not a
risk. Drop any candidate you cannot confirm from the supplied facts. You are
advisory; you only diagnose process risk, never whether the work is correct.`
