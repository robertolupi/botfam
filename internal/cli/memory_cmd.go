package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/memory"
	"github.com/spf13/cobra"
)

// NewMemoryCmd builds `botfam memory` — the shared-memory fact CLI
// (proposal-shared-memory-multi-harness, P1). Facts live as one wiki page each;
// writes go through the store's compare-and-swap publish path.
func NewMemoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "memory",
		Short:         "Read and write the fam's shared memory (wiki-backed facts)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	c.AddCommand(newMemoryWriteCmd(), newMemoryForgetCmd(), newMemoryGetCmd(), newMemoryListCmd())
	return c
}

// openMemoryStore resolves the actor + forge config for the current worktree and
// returns a store over a per-actor clone of the fam wiki, plus the clone dir.
func openMemoryStore() (store *memory.Store, actor, cloneDir string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", "", err
	}
	// ResolveAgentRuntime handles wiki/ and submodule subdirs by stripping the
	// nested git root and re-resolving at the enclosing agent worktree. Direct
	// GitResolver use would return actor="wiki", which is not a declared agent.
	fctx, err := famctx.ResolveAgentRuntime(wd)
	if err != nil {
		return nil, "", "", err
	}
	rf := fctx // alias for readability below; famctx.Context embeds FamIdentity
	client, err := forge.NewClientFromCtx(fctx)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve forge config: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", "", err
	}
	famSlug := rf.Slug
	if famSlug == "" {
		famSlug = "botfam"
	}
	// Per-actor clone so concurrent agents never race on a shared working tree;
	// cross-agent concurrency is mediated by the push/rebase CAS at the remote.
	cloneDir = filepath.Join(home, ".botfam", famSlug, "memory-clone-"+rf.Actor)
	authURL := memory.WikiAuthURL(client.BaseURL, client.Owner, client.Repo, client.Token)
	w, err := memory.CloneWiki(authURL, cloneDir, "")
	if err != nil {
		return nil, "", "", fmt.Errorf("open wiki clone: %w", err)
	}
	return memory.NewStore(w), rf.Actor, cloneDir, nil
}

func today() string { return time.Now().Format("2006-01-02") }

// mergeForUpdate carries an existing fact's state onto a new write so an
// update-in-place doesn't silently lose metadata: it preserves Created, stamps
// Updated, merges the author, and keeps Scope/Type/Concepts/Supersedes the
// caller did not explicitly override on this write (changed reports whether a
// flag was set). Status/Body always come from the new write.
func mergeForUpdate(m, existing memory.Memory, actor string, changed func(string) bool) memory.Memory {
	m.Created = existing.Created
	m.Updated = today()
	m.Authors = memory.SortAuthors(append(existing.Authors, actor))
	if !changed("scope") {
		m.Scope = existing.Scope
	}
	if !changed("type") {
		m.Type = existing.Type
	}
	if !changed("concepts") {
		m.Concepts = existing.Concepts
	}
	if !changed("supersedes") {
		m.Supersedes = existing.Supersedes
	}
	return m
}

func newMemoryWriteCmd() *cobra.Command {
	var title, body, typ, scope, concepts, supersedes string
	c := &cobra.Command{
		Use:           "write --title <t> [--body <b>]",
		Short:         "Create or update a shared fact (body read from stdin if --body omitted)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(title) == "" {
				return errors.New("--title is required")
			}
			if body == "" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				body = strings.TrimSpace(string(b))
			}
			store, actor, _, err := openMemoryStore()
			if err != nil {
				return err
			}
			// Read any existing fact FIRST and fail closed on a read/parse error:
			// overwriting a page we couldn't parse would silently destroy it.
			existing, err := store.Load(title)
			if err != nil {
				return fmt.Errorf("could not read existing fact %q (refusing to overwrite): %w", title, err)
			}
			m := memory.Memory{
				Title:      title,
				Status:     memory.StatusLive,
				Authors:    []string{actor},
				Created:    today(),
				Scope:      orElse(scope, memory.ScopeFam),
				Type:       typ,
				Concepts:   splitCSV(concepts),
				Supersedes: splitCSV(supersedes),
				Body:       body,
			}
			if existing != nil {
				m = mergeForUpdate(m, *existing, actor, cmd.Flags().Changed)
			}
			if err := store.Write(m, actor); err != nil {
				if errors.Is(err, memory.ErrConflict) {
					return fmt.Errorf("%w\nanother agent changed this fact while you were editing; re-read with `botfam memory get --title %q`, reconcile, and retry", err, title)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote memory: %s (%s)\n", memory.Slug(title), title)
			return nil
		},
	}
	c.Flags().StringVar(&title, "title", "", "fact title (required)")
	c.Flags().StringVar(&body, "body", "", "fact body (default: read from stdin)")
	c.Flags().StringVar(&typ, "type", "", "type: user|feedback|project|reference")
	c.Flags().StringVar(&scope, "scope", memory.ScopeFam, "scope: personal|fam|cross-fam")
	c.Flags().StringVar(&concepts, "concepts", "", "comma-separated concept tags")
	c.Flags().StringVar(&supersedes, "supersedes", "", "comma-separated slugs this fact replaces")
	return c
}

func newMemoryForgetCmd() *cobra.Command {
	var title string
	c := &cobra.Command{
		Use:           "forget --title <t>",
		Short:         "Tombstone a fact (Status: Historical), preserving its history",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(title) == "" {
				return errors.New("--title is required")
			}
			store, actor, _, err := openMemoryStore()
			if err != nil {
				return err
			}
			if err := store.Forget(title, actor); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "forgot (tombstoned) memory: %s\n", memory.Slug(title))
			return nil
		},
	}
	c.Flags().StringVar(&title, "title", "", "fact title (required)")
	return c
}

func newMemoryGetCmd() *cobra.Command {
	var title string
	c := &cobra.Command{
		Use:           "get --title <t>",
		Short:         "Print a shared fact's rendered page",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(title) == "" {
				return errors.New("--title is required")
			}
			store, _, _, err := openMemoryStore()
			if err != nil {
				return err
			}
			m, err := store.Load(title)
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("no fact titled %q", title)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), m.Render())
			return err
		},
	}
	c.Flags().StringVar(&title, "title", "", "fact title (required)")
	return c
}

func newMemoryListCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:           "list",
		Short:         "List shared facts (Live only unless --all)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, cloneDir, err := openMemoryStore()
			if err != nil {
				return err
			}
			metas, err := listMemories(cloneDir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, m := range metas {
				if !all && m.Status == memory.StatusHistorical {
					continue
				}
				fmt.Fprintf(out, "%-12s %s\t%s\n", orElse(m.Status, "Live"), m.Slug, m.Title)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "include tombstoned (Historical) facts")
	return c
}

// memoryMeta is a one-line summary for `memory list`.
type memoryMeta struct {
	Slug   string
	Title  string
	Status string
}

// listMemories enumerates memory-*.md pages from the local wiki clone at
// cloneDir (kept fresh by openMemoryStore). The MCP-served `memory-*`
// projection is the discoverable counterpart, P2.
func listMemories(cloneDir string) ([]memoryMeta, error) {
	entries, err := os.ReadDir(cloneDir)
	if err != nil {
		return nil, err
	}
	var metas []memoryMeta
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "memory-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cloneDir, name))
		if err != nil {
			continue
		}
		m, err := memory.Parse(string(data))
		if err != nil {
			continue
		}
		metas = append(metas, memoryMeta{
			Slug:   strings.TrimSuffix(name, ".md"),
			Title:  m.Title,
			Status: m.Status,
		})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Slug < metas[j].Slug })
	return metas, nil
}

func orElse(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
