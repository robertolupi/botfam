package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/robertolupi/botfam/internal/fam"
)

func main() {
	if err := Execute(os.Args[1:]); err != nil {
		// Preserve the legacy error envelope: a structured JSON object when
		// --json is active, otherwise a plain "botfam: <err>" line on stderr.
		if fam.IsJSONOutput() {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
		} else {
			fmt.Fprintln(os.Stderr, "botfam:", err)
		}
		os.Exit(1)
	}
}
