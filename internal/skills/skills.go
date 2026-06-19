// Package skills is the dependency-free leaf that reads a repo's skills/*/SKILL.md
// catalog (name + description from the YAML frontmatter). Both internal/cli (the
// agent-docs command) and internal/mcp (the skills discovery resource) use it,
// so neither imports the other (#311).
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RepoSkill is one repo-local skill discovered under skills/<name>/SKILL.md.
type RepoSkill struct {
	Name        string
	Description string
	Path        string
}

// ReadRepoSkills lists the repo-local skills under <repoRoot>/skills, sorted by
// name then path. A missing skills directory yields no skills (not an error).
func ReadRepoSkills(repoRoot string) ([]RepoSkill, error) {
	skillsDir := filepath.Join(repoRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	var skills []RepoSkill
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

func readRepoSkill(path, rel string) (RepoSkill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RepoSkill{}, nil
		}
		return RepoSkill{}, fmt.Errorf("read %s: %w", rel, err)
	}
	name, desc, err := parseSkillFrontmatter(string(data))
	if err != nil {
		return RepoSkill{}, fmt.Errorf("%s: %w", rel, err)
	}
	return RepoSkill{Name: name, Description: desc, Path: rel}, nil
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
