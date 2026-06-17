package mangle

import "testing"

func TestNameC(t *testing.T) {
	cases := map[string]string{
		"claude-bot":   "/claude_bot",
		"rlupi":        "/rlupi",
		"a/b c":        "/a_b_c",
		"":             "/unknown",
		"  spaced   ":  "/spaced",
		"cAFE1234beef": "/cAFE1234beef",
	}
	for in, want := range cases {
		if got := nameC(in); got != want {
			t.Errorf("nameC(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMTime(t *testing.T) {
	// RFC3339 with offset -> UTC, no offset
	if got := mTime("2026-06-17T08:47:00+02:00"); got != "2026-06-17T06:47:00" {
		t.Errorf("mTime offset = %q, want 2026-06-17T06:47:00", got)
	}
	// empty / zero-value timestamps -> ""
	for _, in := range []string{"", "   ", "0001-01-01T00:00:00Z", "not-a-time"} {
		if got := mTime(in); got != "" {
			t.Errorf("mTime(%q) = %q, want empty", in, got)
		}
	}
}

func TestIDs(t *testing.T) {
	if issueID(272) != "/i272" || prID(385) != "/pr385" {
		t.Errorf("id helpers: %s %s", issueID(272), prID(385))
	}
}
