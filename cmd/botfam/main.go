package main

import (
	"fmt"
	"os"

	"github.com/robertolupi/botfam/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "botfam:", err)
		os.Exit(1)
	}
}
