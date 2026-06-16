package cli

import (
	"encoding/json"
	"io"
)

func unique(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

var jsonOutput bool

func IsJSONOutput() bool {
	return jsonOutput
}

func SetJSONOutput(v bool) {
	jsonOutput = v
}

func writeJSONOutput(out io.Writer, val any) error {
	w := json.NewEncoder(out)
	return w.Encode(map[string]any{
		"ok":     true,
		"result": val,
	})
}

func writeJSONError(out io.Writer, err error) error {
	w := json.NewEncoder(out)
	return w.Encode(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}
