package main

import (
	"fmt"
	"os"

	"github.com/robertolupi/botfam/internal/cli"
)

func main() {
	root := cli.NewRootCmd()
	root.SetArgs(os.Args[1:])
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "botfam:", err)
		os.Exit(1)
	}
}
