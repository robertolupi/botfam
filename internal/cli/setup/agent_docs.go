package setup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/skills"
	"github.com/spf13/cobra"
	"github.com/robertolupi/botfam/internal/cli/cmdutil"
)

var agentDocFiles = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

const agentDocsTemplateText = "# botfam agent harness pointer\n" +
	"\n" +
	"This worktree belongs to a botfam agent.\n" +
	"\n" +
	"1. **Your Name**: Resolved by running `botfam whoami` (or worktree basename).\n" +
	"2. **MCP Onboarding**: Run `resources/read` on `botfam:///docs/start` immediately to orient yourself.\n" +
	"3. **Core Protocol**: The full rules live at `botfam:///docs/protocol` (originally at `doc/collab/PROTOCOL.md`).\n" +
	"4. **Environment Health**: Inspect the health warning blocks at `botfam:///` to ensure your token and client are correctly set up. If the root shows `<unresolved>` (e.g., in system-wide MCP setups), call the `orient` tool with your worktree path (as the `work_dir` argument) to bootstrap.\n" +
	"\n" +
	"## Repo-local Skills\n" +
	"\n" +
	"Generated from `skills/*/SKILL.md`.\n" +
	"\n" +
	"{{- if .Skills }}\n" +
	"{{ range .Skills }}\n" +
	"- `{{ .Name }}`: {{ .Description }}\n" +
	"{{- end }}\n" +
	"{{- else }}\n" +
	"- No repo-local skills found.\n" +
	"{{- end }}\n" +
	"\n" +
	"Refer to the MCP resources above for all operational details.\n"

// RepoSkill and the repo-skills reader moved to the internal/skills leaf (#311).
// Re-exported here so the agent-docs renderer and internal/mcp are unaffected.
type RepoSkill = skills.RepoSkill

// ReadRepoSkills re-exports skills.ReadRepoSkills.
func ReadRepoSkills(repoRoot string) ([]RepoSkill, error) { return skills.ReadRepoSkills(repoRoot) }

// AgentDocsCmd is the thin args/io entry point retained for tests; it builds
// the Cobra command and runs it against args.
func AgentDocsCmd(args []string, out io.Writer) error {
	return cmdutil.RunCobra(NewAgentDocsCmd(), args, out)
}

// NewAgentDocsCmd builds the `botfam agent-docs` Cobra command and its
// generate/check subcommands.
func NewAgentDocsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "agent-docs",
		Short:         "Generate or verify the harness entry docs (AGENTS/CLAUDE/GEMINI)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.AddCommand(
		&cobra.Command{
			Use:           "generate",
			Short:         "Regenerate the harness entry docs from skills/*",
			Args:          cobra.NoArgs,
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := GenerateAgentDocs(famconfig.RepoPath(".")); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Generated %s\n", strings.Join(agentDocFiles, ", "))
				return nil
			},
		},
		&cobra.Command{
			Use:           "check",
			Short:         "Verify the harness entry docs are up to date",
			Args:          cobra.NoArgs,
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				stale, err := CheckAgentDocs(famconfig.RepoPath("."))
				if err != nil {
					return err
				}
				if len(stale) > 0 {
					return fmt.Errorf("agent docs are stale: %s; run botfam agent-docs generate", strings.Join(stale, ", "))
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Agent docs are up to date.")
				return nil
			},
		},
	)
	return c
}

func GenerateAgentDocs(repoRoot string) error {
	content, err := RenderAgentDocs(repoRoot)
	if err != nil {
		return err
	}
	for _, name := range agentDocFiles {
		if err := os.WriteFile(filepath.Join(repoRoot, name), content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func CheckAgentDocs(repoRoot string) ([]string, error) {
	want, err := RenderAgentDocs(repoRoot)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, name := range agentDocFiles {
		path := filepath.Join(repoRoot, name)
		got, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if !bytes.Equal(got, want) {
			stale = append(stale, name)
		}
	}
	return stale, nil
}

func RenderAgentDocs(repoRoot string) ([]byte, error) {
	skills, err := ReadRepoSkills(repoRoot)
	if err != nil {
		return nil, err
	}

	tmplText := agentDocsTemplateText
	tmplPath := filepath.Join(repoRoot, "doc", "template", "AGENTS.tmpl")
	if data, err := os.ReadFile(tmplPath); err == nil {
		tmplText = string(data)
	}

	tmpl, err := template.New("agent-docs").Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("parse agent docs template: %w", err)
	}

	var b bytes.Buffer
	if err := tmpl.Execute(&b, struct {
		Skills []RepoSkill
	}{
		Skills: skills,
	}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
