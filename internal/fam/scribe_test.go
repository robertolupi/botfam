package fam

import (
	"io"
	"strings"
	"testing"
)

func TestScribeCmdNickFlag(t *testing.T) {
	// Empty nick is rejected before any connection attempt.
	err := ScribeCmd([]string{"--nick="}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--nick requires a non-empty value") {
		t.Errorf("empty --nick: got %v, want non-empty-value error", err)
	}

	// Unknown arguments are still rejected.
	err = ScribeCmd([]string{"--nickname", "scribe-dc"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown scribe argument") {
		t.Errorf("unknown arg: got %v, want unknown-argument error", err)
	}
}
