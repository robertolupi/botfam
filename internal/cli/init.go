package cli

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
It creates the specified directory, scaffolds a placeholder fam.toml,
and initializes a fresh Git repository in the "main" subdirectory.
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

	// Scaffold fam.toml if not already present
	tomlPath := filepath.Join(absDir, "fam.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		placeholderTOML := fmt.Sprintf(`name       = %q
slug       = %q
forge_url  = ""
repository = ""

# AI agents - uncomment and configure as needed
# [agent.claude]
# harness    = "claude-code"
# forge_user = "claude-bot"

# [agent.agy]
# harness    = "antigravity"
# forge_user = "agy-bot"

# [agent.codex]
# harness    = "codex"
# forge_user = "codex-bot"

# Humans - uncomment and configure as needed
# [user.rlupi]
# forge_user = "rlupi"
`, name, name)
		if err := os.WriteFile(tomlPath, []byte(placeholderTOML), 0o644); err != nil {
			return fmt.Errorf("failed to scaffold fam.toml: %w", err)
		}
		fmt.Fprintf(out, "Scaffolded placeholder fam.toml at %s\n", tomlPath)
	} else {
		fmt.Fprintf(out, "fam.toml already exists at %s\n", tomlPath)
	}

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
	fmt.Fprintln(out, "  1. Open fam.toml and configure your forge_url, repository, and agents/users roster.")
	fmt.Fprintln(out, "  2. Create the remote repository on your forge (Gitea/Forgejo).")
	fmt.Fprintf(out, "  3. Add the remote in your main checkout:\n     cd %s && git remote add origin <url>\n", mainDir)
	fmt.Fprintf(out, "  4. Run setup to configure your environment and worktrees:\n     botfam setup %s\n", mainDir)

	return nil
}
