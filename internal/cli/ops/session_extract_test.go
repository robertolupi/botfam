package ops

import (
	"strings"
	"testing"
	"time"
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "redact token in Authorization header",
			in:   "Authorization: token abcdef1234567890abcdef1234567890abcdef12",
			want: "Authorization: [REDACTED_TOKEN]",
		},
		{
			name: "redact API key",
			in:   "my_api_key = \"secret-1234567890abcdef\"",
			want: "my_api_key = [REDACTED_CREDENTIAL]",
		},
		{
			name: "redact password",
			in:   "password: 'my-super-secret-password'",
			want: "password: [REDACTED_CREDENTIAL]",
		},
		{
			name: "redact Users paths",
			in:   "Error reading file at /Users/rlupi/src/fams/botfam/wt-agy/cmd/main.go",
			want: "Error reading file at [REDACTED_PATH]",
		},
		{
			name: "redact home paths",
			in:   "Path was /home/rlupi/.botfam/token-botfam-agy",
			want: "Path was [REDACTED_PATH]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecrets(tt.in)
			if got != tt.want {
				t.Errorf("RedactSecrets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseDiffSummary(t *testing.T) {
	diff := `diff --git a/internal/fam/newfam.go b/internal/fam/newfam.go
index 123456..7890ab 100644
--- a/internal/fam/newfam.go
+++ b/internal/fam/newfam.go
@@ -432,4 +432,117 @@ func cleanLegacyMCP() {
+func writeClaudeSettings() {
-func oldMethod() {
`
	summary := parseDiffSummary(diff)
	expected := "- **internal/fam/newfam.go** (+1, -1)\n    - Touched: `func cleanLegacyMCP()`\n"
	if summary != expected {
		t.Errorf("parseDiffSummary() = %q, want %q", summary, expected)
	}
}

func TestDeduplicateEvents(t *testing.T) {
	ts := time.Now()
	events := []*timelineEntry{
		{
			Timestamp: ts,
			Tag:       "[PR #45]",
			Actor:     "agy",
			Action:    "commented",
			Body:      "hello",
			EventID:   1,
		},
		{
			Timestamp: ts,
			Tag:       "[PR #45]",
			Actor:     "agy",
			Action:    "commented",
			Body:      "hello",
			EventID:   1, // Duplicate
		},
		{
			Timestamp: ts,
			Tag:       "[PR #45]",
			Actor:     "agy",
			Action:    "commented",
			Body:      "world",
			EventID:   2,
		},
	}

	deduped := deduplicateEvents(events)
	if len(deduped) != 2 {
		t.Errorf("deduplicateEvents() returned %d items, want 2", len(deduped))
	}
	if deduped[0].Body != "hello" || deduped[1].Body != "world" {
		t.Errorf("deduplicateEvents() output mismatch")
	}
}

func TestSessionExtractInvalidTime(t *testing.T) {
	err := SessionExtract([]string{"--milestone", "test", "--since", "invalid-time"}, nil)
	if err == nil {
		t.Error("expected error for invalid --since format, got nil")
	} else if !strings.Contains(err.Error(), "invalid --since format") {
		t.Errorf("unexpected error message: %v", err)
	}

	err = SessionExtract([]string{"--milestone", "test", "--until", "invalid-time"}, nil)
	if err == nil {
		t.Error("expected error for invalid --until format, got nil")
	} else if !strings.Contains(err.Error(), "invalid --until format") {
		t.Errorf("unexpected error message: %v", err)
	}

	err = SessionExtract([]string{"--milestone", "test", "--snapshot-timestamp", "invalid-time"}, nil)
	if err == nil {
		t.Error("expected error for invalid --snapshot-timestamp format, got nil")
	} else if !strings.Contains(err.Error(), "invalid --snapshot-timestamp format") {
		t.Errorf("unexpected error message: %v", err)
	}
}
