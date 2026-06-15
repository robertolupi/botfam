package metareview

import (
	"fmt"
	"strings"
)

// maxBodyChars bounds the artifact excerpt so the prompt stays compact (the
// model's job is small; it does not need the whole diff).
const maxBodyChars = 4000

// BuildPrompt assembles the compact classification prompt: the glossary, the
// deterministically-gathered candidate facts, and an excerpt of the artifact.
// The model's task is narrow — confirm which candidates are real risks and
// write the one-line evidence — so the prompt foregrounds the candidates.
func BuildPrompt(art Artifact, cands []Candidate) string {
	var b strings.Builder
	b.WriteString(Glossary)
	b.WriteString("\n\n## Artifact under review\n\n")
	fmt.Fprintf(&b, "%s #%d: %s\n\n", strings.ToUpper(art.Kind), art.Number, art.Title)
	b.WriteString(excerpt(art.Body, maxBodyChars))
	if art.Diff != "" {
		b.WriteString("\n\n### Diff excerpt\n\n```diff\n")
		b.WriteString(excerpt(art.Diff, maxBodyChars))
		b.WriteString("\n```")
	}

	b.WriteString("\n\n## Candidate signals the retrieval pass found\n\n")
	if len(cands) == 0 {
		b.WriteString("(none — the deterministic pass found nothing)\n")
	} else {
		b.WriteString("Each line is a candidate the driver detected. For each, decide if it is a")
		b.WriteString(" REAL risk given the facts above. Drop any you cannot confirm.\n\n")
		for i, c := range cands {
			note := ""
			if c.Escalate {
				note = " [judgment-heavy — be conservative]"
			}
			fmt.Fprintf(&b, "%d. %s — %s%s\n", i+1, c.Label, c.Evidence, note)
		}
	}

	b.WriteString("\n## Your task\n\n")
	b.WriteString("Return JSON {\"suggestions\":[{\"label\",\"evidence\",\"confidence\"}]} containing")
	b.WriteString(" ONLY the candidates you confirm as real risks. `evidence` is a one-line citation")
	b.WriteString(" of the specific artifact (the missing file, the superseding Lineage decision, or")
	b.WriteString(" the self-fulfilling assertion). `confidence` is \"high\" or \"low\". If none are real,")
	b.WriteString(" return {\"suggestions\":[]}.\n")
	return b.String()
}

// excerpt trims s to at most n characters, appending an ellipsis when cut.
func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}
