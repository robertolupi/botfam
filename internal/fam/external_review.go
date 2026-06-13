package fam

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

const externalReviewHelp = `Usage:
  botfam external-review [opts] MATERIAL [MATERIAL...]
  botfam external-review --pr <index> [opts]
  botfam external-review --session-file <path> [opts]
  botfam external-review --milestone <name> [opts]

Fan a canonical review prompt + material across one or more LLMs, saving each
raw review out-of-repo under ${BOTFAM_REVIEW_DIR:-~/.botfam/reviews}/<ts>-<slug>;
then spawn a consolidation subagent (do NOT read the raw reviews into context).

Models talk to providers over the OpenAI-compatible chat API (one client for
all). Pick models via repeatable flags (no baked-in defaults):
  --ollama MODEL    local ollama (OLLAMA_HOST, default http://localhost:11434)
  --openai MODEL    OpenAI         (needs OPENAI_API_KEY)
  --gemini MODEL    Gemini         (needs GEMINI_API_KEY)

  --pr <index>         synthesize the material from a Gitea PR (metadata, description,
                       discussion, reviews, unified diff) via the forge; slug pr-<index>.
  --session-file <pat> ingest an extracted milestone session markdown file directly.
  --milestone <name>   automatically extract milestone session and run reviews on it.
  --prompt FILE        canonical prompt (default doc/review/EXTERNAL-REVIEW-PROMPT.md);
                       only text below the "PROMPT BEGINS BELOW THIS LINE" marker is used.
  --out DIR            output dir.

Keys are read from the environment only and never printed. Unreachable ollama or
an unset API key is skipped with a warning, not a hard failure.
`

type erProvider struct {
	name    string   // ollama | openai | gemini
	models  []string // selected models for this provider
	baseURL string
	keyEnv  string // env var holding the API key ("" = none, e.g. ollama)
}

// externalReviewOpts holds the parsed flags for `botfam external-review`.
type externalReviewOpts struct {
	promptFile        string
	outDir            string
	pr                string
	sessionFile       string
	milestoneName     string
	since             string
	until             string
	snapshotTimestamp string
	ollamaHost        string
	redact            bool
	withDiffs         bool
	interactionOnly   bool
	ollama            []string
	openaiM           []string
	gemini            []string
	materials         []string
}

// ExternalReviewCmd is the thin args/io entry point retained for tests; it
// builds the Cobra command and runs it against args.
func ExternalReviewCmd(args []string, out io.Writer) error {
	return runCobra(NewExternalReviewCmd(), args, out)
}

// NewExternalReviewCmd builds the `botfam external-review` Cobra command
// (issue #39). It supersedes the old tools/external-review.sh.
func NewExternalReviewCmd() *cobra.Command {
	var opts externalReviewOpts
	var noRedact bool
	defaultOllamaHost := os.Getenv("OLLAMA_HOST")
	if defaultOllamaHost == "" {
		defaultOllamaHost = "http://localhost:11434"
	}
	c := &cobra.Command{
		Use:           "external-review [flags] [MATERIAL...]",
		Short:         "Fan a review prompt across one or more LLMs",
		Long:          externalReviewHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.redact = opts.redact && !noRedact
			opts.materials = args
			return runExternalReview(opts, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.StringVar(&opts.pr, "pr", "", "synthesize material from a Gitea PR index")
	f.StringVar(&opts.sessionFile, "session-file", "", "ingest an extracted milestone session markdown file")
	f.StringVar(&opts.milestoneName, "milestone", "", "extract a milestone session and review it")
	f.StringArrayVar(&opts.ollama, "ollama", nil, "ollama model to run (repeatable)")
	f.StringArrayVar(&opts.openaiM, "openai", nil, "OpenAI model to run (repeatable; needs OPENAI_API_KEY)")
	f.StringArrayVar(&opts.gemini, "gemini", nil, "Gemini model to run (repeatable; needs GEMINI_API_KEY)")
	f.StringVar(&opts.promptFile, "prompt", "doc/review/EXTERNAL-REVIEW-PROMPT.md", "canonical prompt file")
	f.StringVar(&opts.outDir, "out", "", "output dir (default $BOTFAM_REVIEW_DIR/<ts>-<slug>)")
	f.StringVar(&opts.ollamaHost, "ollama-host", defaultOllamaHost, "ollama host URL")
	f.StringVar(&opts.since, "since", "", "milestone sugar: only events at/after this RFC3339 timestamp")
	f.StringVar(&opts.until, "until", "", "milestone sugar: only events at/before this RFC3339 timestamp")
	f.StringVar(&opts.snapshotTimestamp, "snapshot-timestamp", "", "milestone sugar: freeze the timeline at this RFC3339 timestamp")
	f.BoolVar(&opts.redact, "redact", true, "milestone sugar: scrub secrets/paths before output")
	f.BoolVar(&noRedact, "no-redact", false, "milestone sugar: disable redaction")
	f.BoolVar(&opts.withDiffs, "with-diffs", false, "milestone sugar: append full raw diffs")
	f.BoolVar(&opts.interactionOnly, "interaction-only", false, "milestone sugar: omit the technical diff summary")
	return c
}

func runExternalReview(opts externalReviewOpts, out io.Writer) error {
	promptFile := opts.promptFile
	outDir := opts.outDir
	pr := opts.pr
	sessionFile := opts.sessionFile
	milestoneName := opts.milestoneName
	since := opts.since
	until := opts.until
	redact := opts.redact
	ollamaHost := opts.ollamaHost
	ollama := opts.ollama
	openaiM := opts.openaiM
	gemini := opts.gemini
	materials := opts.materials

	if len(materials) == 0 && pr == "" && sessionFile == "" && milestoneName == "" {
		return fmt.Errorf("no material file(s) and no --pr <index>, --session-file <path>, or --milestone <name> (see --help)")
	}
	if len(ollama)+len(openaiM)+len(gemini) == 0 {
		return fmt.Errorf("no models selected — pass at least one --ollama/--openai/--gemini (see --help)")
	}
	promptText, err := promptBelowMarker(promptFile)
	if err != nil {
		return err
	}

	ts := time.Now().Format("20060102-150405")
	slug := ""
	if pr != "" {
		slug = "pr-" + pr
	} else if milestoneName != "" {
		slug = "milestone-" + slugify(milestoneName)
	} else if sessionFile != "" {
		slug = "session-" + slugify(strings.TrimSuffix(filepath.Base(sessionFile), filepath.Ext(sessionFile)))
	} else if len(materials) > 0 {
		slug = slugify(strings.TrimSuffix(filepath.Base(materials[0]), filepath.Ext(materials[0])))
	} else {
		slug = "session-review"
	}

	if outDir == "" {
		base := os.Getenv("BOTFAM_REVIEW_DIR")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".botfam", "reviews")
		}
		outDir = filepath.Join(base, ts+"-"+slug)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Assemble the material.
	var material strings.Builder
	if milestoneName != "" {
		// Direct milestone sugar: extract first to a temporary file
		tmpDir, err := os.MkdirTemp("", "botfam-milestone-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		tmpFile := filepath.Join(tmpDir, "session.md")

		extractArgs := []string{"--milestone", milestoneName, "--out", tmpFile}
		if since != "" {
			extractArgs = append(extractArgs, "--since", since)
		}
		if until != "" {
			extractArgs = append(extractArgs, "--until", until)
		}
		if !redact {
			extractArgs = append(extractArgs, "--no-redact")
		}
		if opts.withDiffs {
			extractArgs = append(extractArgs, "--with-diffs")
		}
		if opts.interactionOnly {
			extractArgs = append(extractArgs, "--interaction-only")
		}
		if opts.snapshotTimestamp != "" {
			extractArgs = append(extractArgs, "--snapshot-timestamp", opts.snapshotTimestamp)
		}

		var extractOut bytes.Buffer
		if err := sessionExtract(extractArgs, &extractOut); err != nil {
			return fmt.Errorf("milestone extraction failed: %w", err)
		}
		sessionFile = tmpFile
	}

	if sessionFile != "" {
		b, err := os.ReadFile(sessionFile)
		if err != nil {
			return fmt.Errorf("session file not found: %s", sessionFile)
		}
		contentStr := string(b)
		if redact {
			contentStr = redactSecrets(contentStr)
		}
		material.WriteString(contentStr)
		_ = os.WriteFile(filepath.Join(outDir, "session.md"), []byte(contentStr), 0o644)
		fmt.Fprintf(out, "read session file material: %d bytes\n", len(contentStr))
	} else if pr != "" {
		m, err := assemblePRMaterial(pr)
		if err != nil {
			return err
		}
		material.WriteString(m)
		_ = os.WriteFile(filepath.Join(outDir, "pr-"+pr+".md"), []byte(m), 0o644)
		fmt.Fprintf(out, "assembled PR #%s material: %d bytes\n", pr, len(m))
	} else {
		for _, f := range materials {
			b, err := os.ReadFile(f)
			if err != nil {
				return fmt.Errorf("material not found: %s", f)
			}
			fmt.Fprintf(&material, "### %s\n\n%s\n\n", f, b)
		}
	}

	combined := promptText + "\n\n## Material under review\n\n" + material.String()
	if err := os.WriteFile(filepath.Join(outDir, "combined-prompt.txt"), []byte(combined), 0o644); err != nil {
		return err
	}

	providers := []erProvider{
		{name: "ollama", models: ollama, baseURL: strings.TrimSuffix(ollamaHost, "/") + "/v1", keyEnv: ""},
		{name: "openai", models: openaiM, baseURL: "https://api.openai.com/v1", keyEnv: "OPENAI_API_KEY"},
		{name: "gemini", models: gemini, baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", keyEnv: "GEMINI_API_KEY"},
	}

	fmt.Fprintf(out, "running reviews into %s ...\n", outDir)
	var ran []string
	ctx := context.Background()
	for _, p := range providers {
		if len(p.models) == 0 {
			continue
		}
		key := ""
		if p.keyEnv != "" {
			key = os.Getenv(p.keyEnv)
			if key == "" {
				fmt.Fprintf(out, "  %s unset — skipping %d %s model(s)\n", p.keyEnv, len(p.models), p.name)
				continue
			}
		}
		for _, model := range p.models {
			fmt.Fprintf(out, "  %s: %s ...\n", p.name, model)
			text, err := runReview(ctx, p.baseURL, key, model, combined)
			if err != nil {
				fmt.Fprintf(out, "    FAILED (%s:%s): %v\n", p.name, model, err)
				continue
			}
			outFile := filepath.Join(outDir, fmt.Sprintf("review-%s-%s.md", p.name, slugify(model)))
			if err := os.WriteFile(outFile, []byte(text), 0o644); err != nil {
				return err
			}
			ran = append(ran, p.name+":"+model)
			fmt.Fprintf(out, "    -> %s\n", outFile)
		}
	}

	var manifest strings.Builder
	fmt.Fprintf(&manifest, "timestamp: %s\nprompt: %s\n", ts, promptFile)
	if pr != "" {
		fmt.Fprintf(&manifest, "material: PR #%s\n", pr)
	} else {
		fmt.Fprintf(&manifest, "material:\n")
		for _, f := range materials {
			fmt.Fprintf(&manifest, "  - %s\n", f)
		}
	}
	fmt.Fprintf(&manifest, "models:\n")
	for _, r := range ran {
		fmt.Fprintf(&manifest, "  - %s\n", r)
	}
	_ = os.WriteFile(filepath.Join(outDir, "MANIFEST.txt"), []byte(manifest.String()), 0o644)

	fmt.Fprintf(out, "\nwrote %d review(s) to: %s\n", len(ran), outDir)
	fmt.Fprintln(out, "NEXT: spawn a consolidation subagent on this dir; do NOT read the raw reviews into the main context.")
	return nil
}

// runReview calls one model over the OpenAI-compatible chat API.
func runReview(ctx context.Context, baseURL, apiKey, model, prompt string) (string, error) {
	opts := []option.RequestOption{option.WithBaseURL(baseURL)}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	} else {
		opts = append(opts, option.WithAPIKey("none")) // ollama ignores it
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	opts = append(opts, option.WithHTTPClient(httpClient))
	client := openai.NewClient(opts...)
	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}
	return resp.Choices[0].Message.Content, nil
}

// assemblePRMaterial pulls a Gitea PR's metadata, description, discussion,
// reviews, and unified diff into one review-material doc.
func assemblePRMaterial(pr string) (string, error) {
	prNum, err := strconv.Atoi(pr)
	if err != nil {
		return "", fmt.Errorf("invalid --pr %q: %w", pr, err)
	}
	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		if info, err := (Resolver{WorkDir: "."}).Resolve(); err == nil {
			actor = info.Actor
		}
	}
	client, err := forge.NewClient(".", actor)
	if err != nil {
		return "", fmt.Errorf("external-review --pr: %w", err)
	}
	info, err := client.GetPR(prNum)
	if err != nil {
		return "", err
	}
	diff, err := client.GetPRDiff(prNum)
	if err != nil {
		return "", err
	}
	reviews, _ := client.GetPRReviews(prNum)
	comments, _ := client.ListIssueComments(prNum)

	var b strings.Builder
	fmt.Fprintf(&b, "# PR #%d: %s\n", info.Number, info.Title)
	fmt.Fprintf(&b, "- Author: %s\n- %s → %s\n- State: %s\n", info.User.Login, info.Head.Ref, info.Base.Ref, info.State)
	body := strings.TrimSpace(info.Body)
	if body == "" {
		body = "_(no description)_"
	}
	fmt.Fprintf(&b, "\n## Description\n\n%s\n", body)
	fmt.Fprintf(&b, "\n## Discussion (%d comment(s))\n", len(comments))
	for _, c := range comments {
		if t := strings.TrimSpace(c.Body); t != "" {
			fmt.Fprintf(&b, "\n**%s**: %s\n", c.User.Login, t)
		}
	}
	fmt.Fprintf(&b, "\n## Reviews (%d)\n", len(reviews))
	for _, r := range reviews {
		fmt.Fprintf(&b, "\n**%s** [%s]: %s\n", r.User.Login, r.State, strings.TrimSpace(r.Body))
	}
	fmt.Fprintf(&b, "\n## Unified diff\n\n```diff\n%s\n```\n", diff)
	return b.String(), nil
}

func promptBelowMarker(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt file not found: %s", path)
	}
	const marker = "PROMPT BEGINS BELOW THIS LINE"
	s := string(b)
	if i := strings.Index(s, marker); i >= 0 {
		if nl := strings.IndexByte(s[i:], '\n'); nl >= 0 {
			return strings.TrimLeft(s[i+nl+1:], "\n"), nil
		}
	}
	return s, nil
}
