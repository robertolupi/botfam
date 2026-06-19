package famconfig

import "testing"

func TestDetectHarnessFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		environ []string
		want    string
	}{
		{"claudecode flag", []string{"CLAUDECODE=1"}, HarnessClaudeCode},
		{"claude entrypoint", []string{"CLAUDE_CODE_ENTRYPOINT=cli"}, HarnessClaudeCode},
		{"empty value ignored", []string{"CLAUDECODE="}, ""},
		{"unrelated env", []string{"PATH=/usr/bin", "HOME=/x"}, ""},
		{"no env", nil, ""}, // nil would read os.Environ; pass empty instead below
	}
	for _, tc := range cases {
		environ := tc.environ
		if tc.name == "no env" {
			environ = []string{} // avoid reading the real process env in this case
		}
		if got := DetectHarnessFromEnv(environ); got != tc.want {
			t.Errorf("%s: DetectHarnessFromEnv = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDetectHarnessFromClientName(t *testing.T) {
	cases := map[string]string{
		"claude-code": HarnessClaudeCode,
		"Claude Code": HarnessClaudeCode,
		"codex-cli":   HarnessCodex,
		"Antigravity": HarnessAntigravity,
		"gemini-cli":  HarnessAntigravity,
		"some-editor": "",
		"":            "",
		"   ":         "",
	}
	for in, want := range cases {
		if got := DetectHarnessFromClientName(in); got != want {
			t.Errorf("DetectHarnessFromClientName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveHarness(t *testing.T) {
	cases := []struct {
		name       string
		declared   string
		clientName string
		environ    []string
		wantEff    string
		wantSource string
		wantMis    bool
	}{
		{
			name:     "declared alias matches env detection", // the #371 case
			declared: "claude", environ: []string{"CLAUDECODE=1"},
			wantEff: HarnessClaudeCode, wantSource: "env", wantMis: false,
		},
		{
			name:     "clientinfo wins over env",
			declared: "claude-code", clientName: "claude-code", environ: []string{"CLAUDECODE=1"},
			wantEff: HarnessClaudeCode, wantSource: "clientinfo", wantMis: false,
		},
		{
			name:     "declared disagrees with detected",
			declared: "codex", environ: []string{"CLAUDECODE=1"},
			wantEff: HarnessClaudeCode, wantSource: "env", wantMis: true,
		},
		{
			name:     "no signal falls back to declared",
			declared: "codex", environ: []string{},
			wantEff: HarnessCodex, wantSource: "declared", wantMis: false,
		},
		{
			name:     "unknown declared passes through",
			declared: "weird", environ: []string{},
			wantEff: "weird", wantSource: "declared", wantMis: false,
		},
		{
			name:    "nothing at all",
			environ: []string{},
			wantEff: "", wantSource: "declared", wantMis: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveHarness(tc.declared, tc.clientName, tc.environ)
			if got.Effective != tc.wantEff {
				t.Errorf("Effective = %q, want %q", got.Effective, tc.wantEff)
			}
			if got.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.Mismatch != tc.wantMis {
				t.Errorf("Mismatch = %v, want %v", got.Mismatch, tc.wantMis)
			}
		})
	}
}
