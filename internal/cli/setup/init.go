package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/robertolupi/botfam/internal/gitexec"
	"github.com/spf13/cobra"
)

const initHelp = `Usage:
  botfam init [dir]

Initialize a new botfam project (greenfield).
It creates the specified directory and initializes a fresh Git repository in the
"main" subdirectory. Configuration is global (~/.botfam/config.toml); run
'botfam setup <name>' from the main checkout to register the fam.
`

// NewInitCmd builds the `botfam init` Cobra command.
func NewInitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "init [dir]",
		Short:         "Initialize a new botfam project (greenfield)",
		Long:          initHelp,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runInit(dir, cmd.OutOrStdout())
		},
	}
	return c
}

func runInit(dir string, out io.Writer) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to resolve directory path: %w", err)
	}

	name := filepath.Base(absDir)
	if name == "" || name == "/" || name == "." || name == ".." {
		return fmt.Errorf("invalid directory path for project name")
	}

	// Create fam directory
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return fmt.Errorf("failed to create project directory: %w", err)
	}

	// Configuration is now global (~/.botfam/config.toml), not a per-fam
	// fam.toml (#404). `botfam setup <name>` (run from the main checkout) writes
	// the [repo.<name>] stanza; greenfield init just lays out the directory.

	// Initialize Git repository in main/
	mainDir := filepath.Join(absDir, "main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		return fmt.Errorf("failed to create main directory: %w", err)
	}

	if _, err := os.Stat(filepath.Join(mainDir, ".git")); os.IsNotExist(err) {
		if _, err := gitexec.Output(mainDir, "init"); err != nil {
			return fmt.Errorf("failed to initialize git repository: %w", err)
		}
		fmt.Fprintf(out, "Initialized fresh Git repository at %s\n", mainDir)
	} else {
		fmt.Fprintf(out, "Git repository already exists at %s\n", mainDir)
	}

	// Print next steps
	fmt.Fprintln(out, "\nbotfam initialization complete.")
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. Create the remote repository on your forge (Gitea/Forgejo).")
	fmt.Fprintf(out, "  2. Add the remote in your main checkout:\n     cd %s && git remote add origin <url>\n", mainDir)
	fmt.Fprintf(out, "  3. Configure forge_url + global [agent.<name>] roster in ~/.botfam/config.toml.\n")
	fmt.Fprintf(out, "  4. Register this fam (writes the [repo.<name>] stanza):\n     botfam setup %s\n", name)

	return nil
}
