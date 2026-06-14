package fam

import (
	"strings"
	"testing"
)

func TestParseCredentialHelpers(t *testing.T) {
	out := "file:/Users/x/.gitconfig\tosxkeychain\n" +
		"file:/Users/x/wt-claude/.git/config\t!git-credential-botfam"
	got := parseCredentialHelpers(out)
	if strings.Join(got, "|") != "osxkeychain|!git-credential-botfam" {
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

func TestOffendingHelpers(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"keychain leaks", []string{"osxkeychain", "!git-credential-botfam"}, "osxkeychain"},
		{"botfam only is clean", []string{"!git-credential-botfam"}, ""},
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
