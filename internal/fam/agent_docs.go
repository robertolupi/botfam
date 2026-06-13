package fam

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

var agentDocFiles = []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"}

const agentDocsTemplateText = "# botfam fam member — read this first\n" +
	"\n" +
	"This checkout is one agent's **worktree** in a botfam coordination fam.\n" +
	"\n" +
	"1. **Your name** is this worktree's directory basename with any leading\n" +
	"   `wt-` or `botfam-` stripped (`wt-$NAME` → `$NAME`). If in doubt:\n" +
	"   `basename \"$PWD\"`.\n" +
	"2. **Read [doc/collab/PROTOCOL.md](doc/collab/PROTOCOL.md) before your first\n" +
	"   collab call.** It is the single source of truth for identity rules,\n" +
	"   coordination tools, the ccrep change protocol, worktree ownership, and\n" +
	"   platform gotchas.\n" +
	"3. Talk to the fam through the **`botfam`** CLI tool. You can invoke commands\n" +
	"   like `botfam worktree`, `botfam session`, `botfam verify`, etc. directly.\n" +
	"4. **Connect to the IRC server immediately.** To join the conversation, run\n" +
	"   `botfam irc-client <name>` as a background task. A registered nick's pass\n" +
	"   file is found automatically at `~/.botfam/irc-pass-<fam>-<name>` (or the\n" +
	"   legacy `~/.botfam/irc-pass-<name>`); pass `--pass-file` to override.\n" +
	"   Monitor for incoming messages using the\n" +
	"   wake watcher `botfam irc-wait`. See [doc/collab/IRC-OPS.md](doc/collab/IRC-OPS.md)\n" +
	"   for server details and operational recipes.\n" +
	"5. **Sending and reading.** Write lines to `scratch/irc/<name>/in`: a bare\n" +
	"   line goes as text to your fam's main channel; `/msg <target> <text>`\n" +
	"   messages another channel or nick; `/join <#chan>` joins a channel;\n" +
	"   `/raw <cmd>` sends any IRC command. Replies appear in\n" +
	"   `scratch/irc/<name>/log`. If the botfam MCP server is connected, prefer\n" +
	"   the `irc_write` / `irc_read` / `irc_wait` tools — same semantics, no\n" +
	"   shell approval prompts.\n" +
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
	"Keep this file lightweight: substantive rules belong in PROTOCOL.md, never\n" +
	"here. This file is generated from the same source as the other harness files.\n"

type repoSkill struct {
	Name        string
	Description string
	Path        string
}

// AgentDocsCmd dispatches agent-doc generation subcommands.
func AgentDocsCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return printAgentDocsHelp(out)
	}

	repoRoot := RepoPath(".")
	switch args[0] {
	case "generate":
		if len(args) > 1 {
			return fmt.Errorf("unknown argument %q", args[1])
		}
		if err := GenerateAgentDocs(repoRoot); err != nil {
			return err
		}
		fmt.Fprintf(out, "Generated %s\n", strings.Join(agentDocFiles, ", "))
		return nil
	case "check":
		if len(args) > 1 {
			return fmt.Errorf("unknown argument %q", args[1])
		}
		stale, err := CheckAgentDocs(repoRoot)
		if err != nil {
			return err
		}
		if len(stale) > 0 {
			return fmt.Errorf("agent docs are stale: %s; run botfam agent-docs generate", strings.Join(stale, ", "))
		}
		fmt.Fprintln(out, "Agent docs are up to date.")
		return nil
	case "-h", "--help", "help":
		return printAgentDocsHelp(out)
	default:
		return fmt.Errorf("unknown agent-docs command %q", args[0])
	}
}

func printAgentDocsHelp(out io.Writer) error {
	fmt.Fprint(out, `Usage:
  botfam agent-docs generate
  botfam agent-docs check
`)
	return nil
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
	skills, err := readRepoSkills(repoRoot)
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
		Skills []repoSkill
	}{
		Skills: skills,
	}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func readRepoSkills(repoRoot string) ([]repoSkill, error) {
	skillsDir := filepath.Join(repoRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	var skills []repoSkill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rel := filepath.Join("skills", entry.Name(), "SKILL.md")
		skill, err := readRepoSkill(filepath.Join(repoRoot, rel), rel)
		if err != nil {
			return nil, err
		}
		if skill.Name != "" {
			skills = append(skills, skill)
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].Path < skills[j].Path
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

func readRepoSkill(path, rel string) (repoSkill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return repoSkill{}, nil
		}
		return repoSkill{}, fmt.Errorf("read %s: %w", rel, err)
	}
	name, desc, err := parseSkillFrontmatter(string(data))
	if err != nil {
		return repoSkill{}, fmt.Errorf("%s: %w", rel, err)
	}
	return repoSkill{Name: name, Description: desc, Path: rel}, nil
}

func parseSkillFrontmatter(s string) (string, string, error) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", errors.New("missing YAML frontmatter")
	}

	var name, desc string
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			if name == "" {
				return "", "", errors.New("frontmatter missing name")
			}
			if desc == "" {
				return "", "", errors.New("frontmatter missing description")
			}
			return name, desc, nil
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			desc = value
		}
	}
	return "", "", errors.New("unterminated YAML frontmatter")
}
