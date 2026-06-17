package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/gitexec"
	"github.com/robertolupi/botfam/internal/metareview"
	"github.com/robertolupi/botfam/internal/wiki"
	"github.com/spf13/cobra"
)

const metaReviewHelp = `Usage:
  botfam meta-review <issue-or-pr-number> [opts]
  botfam meta-review eval --set <labelled.json> [opts]

Immediate per-artifact process-risk meta-review (botfam#306, the read-only,
harness-agnostic realization of the meta-review skill). A deterministic driver
gathers candidate risk signals — referenced files absent from the tree
(phase-inversion), decisions marked superseded in the wiki Lineage, test
assertions that may re-assert what the code emits (hollow-validation) — then a
local ollama model confirms and phrases them. The driver posts ONE advisory
comment with suggested risk/* labels + cited evidence.

Read-only except for that one comment (and, with --apply-labels, additive
high-confidence risk/* labels). Code never leaves the box: classification runs
on a local model. Output is reproducible (temperature 0 + fixed --seed).

Mechanical risks (phase-inversion, superseded) run on the local --ollama model.
speculative routes to --escalate when set, else the local model at low
confidence. hollow-validation runs ONLY when --escalate is set: the deterministic
pass cannot tell it from an ordinary literal assertion, so without a stronger
model to do the real reasoning it is skipped rather than surfaced as noise.

  --ollama MODEL    local model that confirms+phrases (default gpt-oss:20b)
  --escalate MODEL  stronger local model for judgment-heavy risks (optional)
  --ollama-host URL ollama host (default $OLLAMA_HOST or http://localhost:11434)
  --seed N          model seed for reproducibility (default 7)
  --apply-labels    also apply high-confidence risk/* labels (additive)
  --dry-run         print the comment instead of posting; no writes at all

eval: run the driver over a self-contained labelled set (JSON array or
{"cases":[...]}) and report per-risk precision/recall vs. the confirmed labels.
`

// defaultMetaReviewModel is the local model the driver classifies with unless
// --ollama overrides it. gpt-oss:20b is the best accuracy/cost point on the
// seed eval set (botfam#306): it confirms every risk including the hard
// hollow-validation case, at ~5x the speed and ~10GB less VRAM than the larger
// qwen models. Point --escalate at a bigger model if a harder set later needs
// it.
const defaultMetaReviewModel = "gpt-oss:20b"

type metaReviewOpts struct {
	ollamaModel   string
	escalateModel string
	ollamaHost    string
	seed          int64
	applyLabels   bool
	dryRun        bool
	// eval-only
	setFile string
}

func defaultOllamaHost() string {
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		return h
	}
	return "http://localhost:11434"
}

// NewMetaReviewCmd builds the `botfam meta-review` command and its `eval`
// subcommand (botfam#306).
func NewMetaReviewCmd() *cobra.Command {
	var opts metaReviewOpts
	c := &cobra.Command{
		Use:           "meta-review <issue-or-pr-number>",
		Short:         "Advisory per-artifact process-risk review via a local ollama model",
		Long:          metaReviewHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: RunWithFamCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
			num, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid issue/PR number %q: %w", args[0], err)
			}
			client, err := forge.NewClient(ctx)
			if err != nil {
				return fmt.Errorf("meta-review: %w", err)
			}
			return runMetaReview(num, opts, client, cmd.OutOrStdout())
		}),
	}
	addMetaReviewModelFlags(c, &opts)
	c.Flags().BoolVar(&opts.applyLabels, "apply-labels", false, "also apply high-confidence risk/* labels (additive)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print the comment instead of posting")

	c.AddCommand(newMetaReviewEvalCmd(&opts))
	return c
}

func newMetaReviewEvalCmd(opts *metaReviewOpts) *cobra.Command {
	c := &cobra.Command{
		Use:           "eval --set <labelled.json>",
		Short:         "Score the driver against a labelled set (per-risk precision/recall)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMetaReviewEval(*opts, cmd.OutOrStdout())
		},
	}
	addMetaReviewModelFlags(c, opts)
	c.Flags().StringVar(&opts.setFile, "set", "", "labelled JSON set (required)")
	return c
}

func addMetaReviewModelFlags(c *cobra.Command, opts *metaReviewOpts) {
	f := c.Flags()
	f.StringVar(&opts.ollamaModel, "ollama", defaultMetaReviewModel, "local ollama model that confirms+phrases")
	f.StringVar(&opts.escalateModel, "escalate", "", "stronger local model for judgment-heavy risks (optional)")
	f.StringVar(&opts.ollamaHost, "ollama-host", defaultOllamaHost(), "ollama host URL")
	f.Int64Var(&opts.seed, "seed", 7, "model seed for reproducibility")
}

// classifiers builds the local (required) and escalation (optional) classifiers
// from the options, plus the attribution tag for the comment footer.
func (o metaReviewOpts) classifiers() (local, escalate metareview.Classifier, tag string, err error) {
	if o.ollamaModel == "" {
		return nil, nil, "", fmt.Errorf("--ollama MODEL is required (the local model that classifies)")
	}
	base := strings.TrimSuffix(o.ollamaHost, "/") + "/v1"
	local = &metareview.OllamaClassifier{BaseURL: base, Model: o.ollamaModel, Seed: o.seed}
	tag = fmt.Sprintf("ollama:%s seed=%d", o.ollamaModel, o.seed)
	if o.escalateModel != "" {
		escalate = &metareview.OllamaClassifier{BaseURL: base, Model: o.escalateModel, Seed: o.seed}
		tag += fmt.Sprintf(" escalate=ollama:%s", o.escalateModel)
	}
	return local, escalate, tag, nil
}

func runMetaReview(num int, opts metaReviewOpts, client *forge.Client, out io.Writer) error {
	local, escalate, tag, err := opts.classifiers()
	if err != nil {
		return err
	}

	corpus, err := newRepoCorpus(client)
	if err != nil {
		return err
	}

	_, err = metareview.Run(context.Background(), metareview.Options{
		Number:      num,
		Forge:       client,
		Corpus:      corpus,
		Local:       local,
		Escalate:    escalate,
		ModelTag:    tag,
		ApplyLabels: opts.applyLabels,
		DryRun:      opts.dryRun,
		Out:         out,
	})
	return err
}

func runMetaReviewEval(opts metaReviewOpts, out io.Writer) error {
	if opts.setFile == "" {
		return fmt.Errorf("--set <labelled.json> is required")
	}
	local, escalate, _, err := opts.classifiers()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(opts.setFile)
	if err != nil {
		return fmt.Errorf("read labelled set: %w", err)
	}
	cases, err := metareview.LoadEvalCases(data)
	if err != nil {
		return err
	}
	rep, err := metareview.Eval(context.Background(), cases, local, escalate, out)
	if err != nil {
		return err
	}
	fmt.Fprint(out, metareview.FormatReport(rep))
	return nil
}

// repoCorpus implements metareview.Corpus against the live checkout (file
// existence) and the wiki Lineage page (fetched once).
type repoCorpus struct {
	root    string
	lineage string
}

func (c repoCorpus) FileExists(p string) bool {
	_, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(p)))
	return err == nil
}

func (c repoCorpus) Lineage() string { return c.lineage }

func newRepoCorpus(client *forge.Client) (repoCorpus, error) {
	root, err := gitexec.One(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return repoCorpus{}, fmt.Errorf("locate repo root: %w", err)
	}
	corpus := repoCorpus{root: strings.TrimSpace(root)}
	// Lineage is best-effort: if the wiki is unreachable, superseded detection
	// simply finds nothing rather than failing the run.
	if provider, err := wiki.Resolve(client, filepath.Join(corpus.root, "wiki")); err == nil {
		if page, err := provider.Page("Lineage"); err == nil {
			corpus.lineage = page.Content
		}
	}
	return corpus, nil
}
