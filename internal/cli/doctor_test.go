package cli

import (
	"strings"
	"testing"
)

func TestParseCredentialHelpers(t *testing.T) {
	out := "file:/Users/x/.gitconfig\tosxkeychain\n" +
		"file:/Users/x/wt-claude/.git/config\t!botfam credential"
	got := parseCredentialHelpers(out)
	if strings.Join(got, "|") != "osxkeychain|!botfam credential" {
		t.Errorf("parseCredentialHelpers = %v", got)
	}

	// No tab (plain value) and blank lines are handled.
	got = parseCredentialHelpers("osxkeychain\n\n")
	if strings.Join(got, "|") != "osxkeychain" {
		t.Errorf("parseCredentialHelpers(plain) = %v", got)
	}

	if len(parseCredentialHelpers("")) != 0 {
		t.Errorf("empty output should yield no helpers")
	}
}

func TestEvaluateGitIdentity(t *testing.T) {
	cases := []struct {
		name       string
		actor      string
		userName   string
		email      string
		wantStatus string
	}{
		{"empty name fails", "claude", "", "x@y.z", doctorFail},
		{"whitespace name fails", "claude", "   ", "x@y.z", doctorFail},
		{"mismatch warns", "claude", "agy", "x@y.z", doctorWarn},
		{"missing email warns", "claude", "claude", "", doctorWarn},
		{"match with email ok", "claude", "claude", "roberto.lupi+claude@gmail.com", doctorOK},
		{"unresolved actor with name+email ok", "", "claude", "x@y.z", doctorOK},
		{"unresolved actor empty name fails", "", "", "", doctorFail},
	}
	for _, tc := range cases {
		got := evaluateGitIdentity(tc.actor, tc.userName, tc.email)
		if got.status != tc.wantStatus {
			t.Errorf("%s: evaluateGitIdentity(%q,%q,%q) status = %q, want %q",
				tc.name, tc.actor, tc.userName, tc.email, got.status, tc.wantStatus)
		}
		if got.status != doctorOK && got.fix == "" {
			t.Errorf("%s: non-ok check should carry a fix hint", tc.name)
		}
	}
}

func TestOffendingHelpers(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"keychain leaks", []string{"osxkeychain", "!botfam credential"}, "osxkeychain"},
		{"botfam only is clean", []string{"!botfam credential"}, ""},
		{"plain botfam name is clean", []string{"botfam"}, ""},
		{"empty reset ignored", []string{"", "botfam"}, ""},
		{"multiple inherited", []string{"osxkeychain", "store"}, "osxkeychain,store"},
		{"no helpers is clean", nil, ""},
	}
	for _, tc := range cases {
		got := strings.Join(offendingHelpers(tc.in), ",")
		if got != tc.want {
			t.Errorf("%s: offendingHelpers(%v) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
