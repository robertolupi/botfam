package cli

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

	// Unknown flags are still rejected (Cobra reports them as "unknown flag").
	err = ScribeCmd([]string{"--nickname", "scribe-dc"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("unknown arg: got %v, want unknown-flag error", err)
	}
}
