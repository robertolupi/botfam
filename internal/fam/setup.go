package fam

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type Registry struct {
	Name         string
	Slug         string
	RootSet      []string
	Origin       string
	Roster       []string
	Channels     []string
	RepoPaths    []string
	ObjectStores []string
	CreatedAt    string
}

// Setup is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func Setup(args []string, out io.Writer) error {
	return runCobra(NewSetupCmd(), args, out)
}

// NewSetupCmd builds the `botfam setup` Cobra command.
func NewSetupCmd() *cobra.Command {
	var agentsCSV string
	var force bool
	c := &cobra.Command{
		Use:           "setup <project> --agents alice,bob [--force]",
		Short:         "Configure an existing botfam project (registry, worktrees, docs)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(args[0], splitCSV(agentsCSV), force, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&agentsCSV, "agents", "", "comma-separated agent names")
	c.Flags().BoolVar(&force, "force", false, "proceed even if the registry already exists with other object stores")
	return c
}

func runSetup(project string, agents []string, force bool, out io.Writer) error {
	if project == "" {
		return fmt.Errorf("project name is required")
	}
	for _, agent := range agents {
		if err := validateSetupName("agent", agent); err != nil {
			return err
		}
	}
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	stores, err := GitObjectStores(".")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(info.Root, 0o755); err != nil {
		return err
	}
	regPath := filepath.Join(info.Root, "fam.toml")
	reg := Registry{}
	if _, err := os.Stat(regPath); err == nil {
		reg, err = ReadRegistry(regPath)
		if err != nil {
			return err
		}
		if !force && !hasAny(reg.ObjectStores, stores) {
			return fmt.Errorf("%s already exists and this repo is not a registered member; use --force, COLLAB_ROOT, or BOTFAM_FAM deliberately", info.Root)
		}
	}
	if reg.Name == "" {
		reg.Name = project
		reg.RootSet = info.RootSet
		reg.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	reg.Roster = unique(append(reg.Roster, agents...))
	reg.RepoPaths = unique(append(reg.RepoPaths, RepoPath(".")))
	reg.ObjectStores = unique(append(reg.ObjectStores, stores...))
	if err := WriteRegistry(regPath, reg); err != nil {
		return err
	}
	for _, agent := range agents {
		for _, sub := range []string{"new", "processing", "cur", "expired"} {
			if err := os.MkdirAll(filepath.Join(info.Root, agent, sub), 0o755); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(info.Root, "tmp"), 0o755); err != nil {
		return err
	}
	for _, sub := range []string{"open", "claimed", "done"} {
		if err := os.MkdirAll(filepath.Join(info.Root, "tasks", sub), 0o755); err != nil {
			return err
		}
	}
	if err := createProjectSymlink(project, info.Root); err != nil {
		return err
	}
	fmt.Fprintf(out, "botfam root: %s\n", info.Root)
	return nil
}

func EnsureMembership(root string, explicit bool, workDir string) error {
	if explicit {
		return os.MkdirAll(root, 0o755)
	}
	reg, err := ReadRegistry(filepath.Join(root, "fam.toml"))
	if err != nil {
		return fmt.Errorf("fam root %s is not set up or readable; run botfam setup", root)
	}
	stores, err := GitObjectStores(workDir)
	if err != nil {
		return err
	}
	if hasAny(reg.ObjectStores, stores) {
		return nil
	}
	return fmt.Errorf("repo object store is not registered for fam root %s; refusing unverified membership", root)
}

func ReadRegistry(path string) (Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Registry{}, err
	}
	defer f.Close()
	reg := Registry{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "name":
			reg.Name = parseString(v)
		case "slug":
			reg.Slug = parseString(v)
		case "origin":
			reg.Origin = parseString(v)
		case "created_at":
			reg.CreatedAt = parseString(v)
		case "root_set":
			reg.RootSet = parseArray(v)
		case "roster":
			reg.Roster = parseArray(v)
		case "channels":
			reg.Channels = parseArray(v)
		case "repo_paths":
			reg.RepoPaths = parseArray(v)
		case "object_stores":
			reg.ObjectStores = parseArray(v)
		}
	}
	return reg, sc.Err()
}

func WriteRegistry(path string, reg Registry) error {
	var b strings.Builder
	fmt.Fprintf(&b, "name = %q\n", reg.Name)
	if reg.Slug != "" {
		fmt.Fprintf(&b, "slug = %q\n", reg.Slug)
	}
	fmt.Fprintf(&b, "created_at = %q\n", reg.CreatedAt)
	writeArray(&b, "root_set", reg.RootSet)
	writeArray(&b, "roster", reg.Roster)
	if len(reg.Channels) > 0 {
		writeArray(&b, "channels", reg.Channels)
	}
	writeArray(&b, "repo_paths", reg.RepoPaths)
	writeArray(&b, "object_stores", reg.ObjectStores)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func createProjectSymlink(project, target string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := validateSetupName("project", project); err != nil {
		return err
	}
	link := filepath.Join(home, ".botfam", project)
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return nil
	}
	_ = os.Remove(link)
	return os.Symlink(target, link)
}

func validateSetupName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	for _, r := range name {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("invalid %s %q: use letters, digits, underscore, or dash", kind, name)
		}
	}
	return nil
}

func writeArray(b *strings.Builder, key string, vals []string) {
	fmt.Fprintf(b, "%s = [", key)
	for i, v := range vals {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", v)
	}
	b.WriteString("]\n")
}

func parseString(v string) string {
	return strings.Trim(strings.TrimSpace(v), `"`)
}

func parseArray(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := []string{}
	for _, p := range parts {
		out = append(out, parseString(p))
	}
	return out
}

func splitCSV(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func hasAny(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if set[x] {
			return true
		}
	}
	return false
}
