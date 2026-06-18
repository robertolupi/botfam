package review

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/cli/ops"
	"github.com/robertolupi/botfam/internal/famconfig"
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
  --ollama MODEL     local ollama    (OLLAMA_HOST, default http://localhost:11434)
  --lmstudio MODEL   local LM Studio  (LMSTUDIO_HOST, default http://localhost:1234)
  --openai MODEL     OpenAI           (needs OPENAI_API_KEY)
  --gemini MODEL     Gemini           (needs GEMINI_API_KEY)
  --anthropic MODEL  Anthropic        (needs ANTHROPIC_API_KEY)

  --pr <index>         synthesize the material from a Gitea PR (metadata, description,
                       discussion, reviews, unified diff) via the forge; slug pr-<index>.
  --session-file <pat> ingest an extracted milestone session markdown file directly.
  --milestone <name>   automatically extract milestone session and run reviews on it.
  --wiki PAGE          review a wiki page by name, read from the local wiki checkout
                       ($BOTFAM_WIKI_DIR or ./wiki); repeatable.
  --prompt FILE        canonical prompt (default doc/review/EXTERNAL-REVIEW-PROMPT.md);
                       only text below the "PROMPT BEGINS BELOW THIS LINE" marker is used.
  --design             shortcut for the adversarial design-review prompt
                       (doc/review/DESIGN-REVIEW-PROMPT.md), for reviewing a spec/page.
  --secrets FILE       load provider API keys from a dotenv-style KEY=VALUE file instead
                       of the environment (e.g. OPENAI_API_KEY=...). Keys are never printed.
  --out DIR            output dir.

API keys are resolved in order: --secrets FILE > the [secrets] stanza in
~/.botfam/config.toml > the environment. They are never printed. An unreachable
local host or an unset API key is skipped with a warning, not a hard failure.
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
	allowZeroReviews  bool
	design            bool
	secretsFile       string
	configSecrets     map[string]string // [secrets] from ~/.botfam/config.toml, seeded by the command (#438)
	lmstudioHost      string
	wikiDir           string
	ollama            []string
	lmstudio          []string
	openaiM           []string
	gemini            []string
	anthropic         []string
	wiki              []string
	materials         []string
}

// defaultDesignPrompt is the canonical adversarial design-review prompt,
// selected by --design (vs the session-review default).
const defaultDesignPrompt = "doc/review/DESIGN-REVIEW-PROMPT.md"

// loadSecrets parses a dotenv-style KEY=VALUE file into a map. Blank lines and
// '#' comments are skipped; surrounding quotes on the value are stripped. Values
// are never logged. It is the file-backed bridge to the #393 secret store: keys
// stay out of the environment, the shell history, and the agent's context.
func loadSecrets(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets file: %w", err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" {
			m[k] = v
		}
	}
	return m, nil
}

// mergeSecrets assembles the provider-key map from the `[secrets]` config stanza
// overlaid by an optional --secrets dotenv file (the file wins). Callers fall
// back to the environment for any key not present here, giving the precedence:
// --secrets file > config [secrets] > environment. Values are never logged.
func mergeSecrets(configSecrets map[string]string, secretsFile string) (map[string]string, error) {
	m := map[string]string{}
	for k, v := range configSecrets {
		if v != "" {
			m[k] = v
		}
	}
	if secretsFile != "" {
		s, err := loadSecrets(secretsFile)
		if err != nil {
			return nil, err
		}
		for k, v := range s {
			m[k] = v
		}
	}
	return m, nil
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
	defaultLMStudioHost := os.Getenv("LMSTUDIO_HOST")
	if defaultLMStudioHost == "" {
		defaultLMStudioHost = "http://localhost:1234"
	}
	c := &cobra.Command{
		Use:           "external-review [flags] [MATERIAL...]",
		Short:         "Fan a review prompt across one or more LLMs",
		Long:          externalReviewHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: cmdutil.RunWithFamCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
			opts.redact = opts.redact && !noRedact
			opts.materials = args
			// --design selects the design prompt unless --prompt was set explicitly.
			if opts.design && !cmd.Flags().Changed("prompt") {
				opts.promptFile = defaultDesignPrompt
			}
			// Provider API keys from the operator's `[secrets]` config stanza (#438).
			// Read at the command layer so runExternalReview stays free of global
			// config I/O (keeps its unit tests hermetic). A missing/unreadable
			// config is non-fatal — keys then come from --secrets or the env.
			if cfg, err := famconfig.LoadConfig(); err == nil {
				opts.configSecrets = cfg.Secrets
			}
			return runExternalReview(ctx, opts, cmd.OutOrStdout())
		}),
	}
	f := c.Flags()
	f.StringVar(&opts.pr, "pr", "", "synthesize material from a Gitea PR index")
	f.StringVar(&opts.sessionFile, "session-file", "", "ingest an extracted milestone session markdown file")
	f.StringVar(&opts.milestoneName, "milestone", "", "extract a milestone session and review it")
	f.StringArrayVar(&opts.ollama, "ollama", nil, "ollama model to run (repeatable)")
	f.StringArrayVar(&opts.lmstudio, "lmstudio", nil, "LM Studio model to run (repeatable)")
	f.StringArrayVar(&opts.openaiM, "openai", nil, "OpenAI model to run (repeatable; needs OPENAI_API_KEY)")
	f.StringArrayVar(&opts.gemini, "gemini", nil, "Gemini model to run (repeatable; needs GEMINI_API_KEY)")
	f.StringArrayVar(&opts.anthropic, "anthropic", nil, "Anthropic model to run (repeatable; needs ANTHROPIC_API_KEY)")
	f.StringArrayVar(&opts.wiki, "wiki", nil, "review a wiki page by name from the local checkout (repeatable)")
	f.StringVar(&opts.promptFile, "prompt", "doc/review/EXTERNAL-REVIEW-PROMPT.md", "canonical prompt file")
	f.BoolVar(&opts.design, "design", false, "use the adversarial design-review prompt ("+defaultDesignPrompt+")")
	f.StringVar(&opts.secretsFile, "secrets", "", "load provider API keys from a dotenv-style file instead of the environment")
	f.StringVar(&opts.outDir, "out", "", "output dir (default $BOTFAM_REVIEW_DIR/<ts>-<slug>)")
	f.StringVar(&opts.ollamaHost, "ollama-host", defaultOllamaHost, "ollama host URL")
	f.StringVar(&opts.lmstudioHost, "lmstudio-host", defaultLMStudioHost, "LM Studio host URL")
	f.StringVar(&opts.wikiDir, "wiki-dir", "", "local wiki checkout dir (default $BOTFAM_WIKI_DIR or ./wiki)")
	f.StringVar(&opts.since, "since", "", "milestone sugar: only events at/after this RFC3339 timestamp")
	f.StringVar(&opts.until, "until", "", "milestone sugar: only events at/before this RFC3339 timestamp")
	f.StringVar(&opts.snapshotTimestamp, "snapshot-timestamp", "", "milestone sugar: freeze the timeline at this RFC3339 timestamp")
	f.BoolVar(&opts.redact, "redact", true, "milestone sugar: scrub secrets/paths before output")
	f.BoolVar(&noRedact, "no-redact", false, "milestone sugar: disable redaction")
	f.BoolVar(&opts.withDiffs, "with-diffs", false, "milestone sugar: append full raw diffs")
	f.BoolVar(&opts.interactionOnly, "interaction-only", false, "milestone sugar: omit the technical diff summary")
	f.BoolVar(&opts.allowZeroReviews, "allow-zero-reviews", false, "succeed even if no model review was produced (dry runs)")
	return c
}

func runExternalReview(ctx context.Context, opts externalReviewOpts, out io.Writer) error {
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
	allowZeroReviews := opts.allowZeroReviews

	// Resolve --wiki PAGE names to files in the local wiki checkout and treat
	// them as ordinary material.
	if len(opts.wiki) > 0 {
		wikiDir := opts.wikiDir
		if wikiDir == "" {
			wikiDir = os.Getenv("BOTFAM_WIKI_DIR")
		}
		if wikiDir == "" {
			wikiDir = "wiki"
		}
		for _, page := range opts.wiki {
			name := strings.TrimSuffix(page, ".md")
			path := filepath.Join(wikiDir, name+".md")
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("wiki page %q not found at %s (set --wiki-dir or $BOTFAM_WIKI_DIR)", page, path)
			}
			materials = append(materials, path)
		}
	}

	// API keys, in precedence order: --secrets file > config [secrets] > env.
	// Values are never printed.
	secrets, err := mergeSecrets(opts.configSecrets, opts.secretsFile)
	if err != nil {
		return err
	}
	lookupKey := func(env string) string {
		if v, ok := secrets[env]; ok && v != "" {
			return v
		}
		return os.Getenv(env)
	}

	if len(materials) == 0 && pr == "" && sessionFile == "" && milestoneName == "" {
		return fmt.Errorf("no material — pass MATERIAL file(s), --wiki <page>, --pr <index>, --session-file <path>, or --milestone <name> (see --help)")
	}
	if len(ollama)+len(opts.lmstudio)+len(openaiM)+len(gemini)+len(opts.anthropic) == 0 {
		return fmt.Errorf("no models selected — pass at least one --ollama/--lmstudio/--openai/--gemini/--anthropic (see --help)")
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
		slug = "milestone-" + ops.Slugify(milestoneName)
	} else if sessionFile != "" {
		slug = "session-" + ops.Slugify(strings.TrimSuffix(filepath.Base(sessionFile), filepath.Ext(sessionFile)))
	} else if len(materials) > 0 {
		slug = ops.Slugify(strings.TrimSuffix(filepath.Base(materials[0]), filepath.Ext(materials[0])))
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
		if err := ops.SessionExtract(extractArgs, &extractOut); err != nil {
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
			contentStr = ops.RedactSecrets(contentStr)
		}
		material.WriteString(contentStr)
		if err := os.WriteFile(filepath.Join(outDir, "session.md"), []byte(contentStr), 0o644); err != nil {
			return fmt.Errorf("failed to write session.md: %w", err)
		}
		fmt.Fprintf(out, "read session file material: %d bytes\n", len(contentStr))
	} else if pr != "" {
		m, err := assemblePRMaterial(ctx, pr)
		if err != nil {
			return err
		}
		material.WriteString(m)
		if err := os.WriteFile(filepath.Join(outDir, "pr-"+pr+".md"), []byte(m), 0o644); err != nil {
			return fmt.Errorf("failed to write pr-%s.md: %w", pr, err)
		}
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
		{name: "lmstudio", models: opts.lmstudio, baseURL: strings.TrimSuffix(opts.lmstudioHost, "/") + "/v1", keyEnv: ""},
		{name: "openai", models: openaiM, baseURL: "https://api.openai.com/v1", keyEnv: "OPENAI_API_KEY"},
		{name: "gemini", models: gemini, baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", keyEnv: "GEMINI_API_KEY"},
		{name: "anthropic", models: opts.anthropic, baseURL: "https://api.anthropic.com/v1", keyEnv: "ANTHROPIC_API_KEY"},
	}

	fmt.Fprintf(out, "running reviews into %s ...\n", outDir)

	// Each review result; collected concurrently then reported deterministically.
	type reviewResult struct {
		provider  string
		model     string
		outFile   string
		reviewErr error // model call failed — non-fatal, reported as a skipped review
		writeErr  error // writing the review file failed — treated as a command error
	}

	// runOne performs one model review and records its outcome under mu. Output
	// is not written from goroutines (that would race the writer, cf. #75); the
	// caller prints results after all goroutines finish.
	var mu sync.Mutex
	var results []reviewResult
	runOne := func(p erProvider, key, model string) {
		res := reviewResult{provider: p.name, model: model}
		text, err := runReview(ctx, p.baseURL, key, model, combined)
		if err != nil {
			res.reviewErr = err
		} else {
			res.outFile = filepath.Join(outDir, fmt.Sprintf("review-%s-%s.md", p.name, ops.Slugify(model)))
			if werr := os.WriteFile(res.outFile, []byte(text), 0o644); werr != nil {
				res.writeErr = werr
			}
		}
		mu.Lock()
		results = append(results, res)
		mu.Unlock()
	}

	// Remote providers (openai, gemini) are parallelized per-model; local ollama
	// runs its models sequentially (a single GPU host can't serve concurrent
	// requests well) but still concurrently with the remotes (issue #54).
	var wg sync.WaitGroup
	for _, p := range providers {
		if len(p.models) == 0 {
			continue
		}
		key := ""
		if p.keyEnv != "" {
			key = lookupKey(p.keyEnv)
			if key == "" {
				fmt.Fprintf(out, "  %s unset — skipping %d %s model(s)\n", p.keyEnv, len(p.models), p.name)
				continue
			}
		}
		fmt.Fprintf(out, "  %s: dispatching %d model(s)\n", p.name, len(p.models))
		if p.keyEnv == "" {
			// Local provider (ollama / lmstudio): serialize its own models in one
			// goroutine (a single GPU host can't serve concurrent requests well).
			p, key := p, key
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, model := range p.models {
					runOne(p, key, model)
				}
			}()
		} else {
			for _, model := range p.models {
				p, key, model := p, key, model
				wg.Add(1)
				go func() {
					defer wg.Done()
					runOne(p, key, model)
				}()
			}
		}
	}
	wg.Wait()

	// Report deterministically (results arrive in nondeterministic order).
	sort.Slice(results, func(i, j int) bool {
		if results[i].provider != results[j].provider {
			return results[i].provider < results[j].provider
		}
		return results[i].model < results[j].model
	})
	var ran []string
	var firstWriteErr error
	for _, r := range results {
		switch {
		case r.writeErr != nil:
			fmt.Fprintf(out, "    WRITE FAILED (%s:%s): %v\n", r.provider, r.model, r.writeErr)
			if firstWriteErr == nil {
				firstWriteErr = fmt.Errorf("failed to write review for %s:%s: %w", r.provider, r.model, r.writeErr)
			}
		case r.reviewErr != nil:
			fmt.Fprintf(out, "    FAILED (%s:%s): %v\n", r.provider, r.model, r.reviewErr)
		default:
			ran = append(ran, r.provider+":"+r.model)
			fmt.Fprintf(out, "    %s:%s -> %s\n", r.provider, r.model, r.outFile)
		}
	}
	if firstWriteErr != nil {
		return firstWriteErr
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
	if err := os.WriteFile(filepath.Join(outDir, "MANIFEST.txt"), []byte(manifest.String()), 0o644); err != nil {
		return fmt.Errorf("failed to write MANIFEST.txt: %w", err)
	}

	fmt.Fprintf(out, "\nwrote %d review(s) to: %s\n", len(ran), outDir)
	if len(ran) == 0 && !allowZeroReviews {
		return fmt.Errorf("no model reviews were produced (every provider was skipped or failed); "+
			"check API keys and model availability, or pass --allow-zero-reviews for a dry run. output dir: %s", outDir)
	}
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
	httpClient := &http.Client{Timeout: 300 * time.Second}
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
func assemblePRMaterial(ctx context.Context, pr string) (string, error) {
	prNum, err := strconv.Atoi(pr)
	if err != nil {
		return "", fmt.Errorf("invalid --pr %q: %w", pr, err)
	}
	client, err := forge.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("external-review --pr: %w", err)
	}
	info, err := client.GetPR(ctx, prNum)
	if err != nil {
		return "", err
	}
	diff, err := client.GetPRDiff(ctx, prNum)
	if err != nil {
		return "", err
	}
	reviews, _ := client.GetPRReviews(ctx, prNum)
	comments, _ := client.ListIssueComments(ctx, prNum)

	var b strings.Builder
	author := ""
	if info.Poster != nil {
		author = info.Poster.UserName
	}
	headRef, baseRef := "", ""
	if info.Head != nil {
		headRef = info.Head.Ref
	}
	if info.Base != nil {
		baseRef = info.Base.Ref
	}
	fmt.Fprintf(&b, "# PR #%d: %s\n", info.Index, info.Title)
	fmt.Fprintf(&b, "- Author: %s\n- %s → %s\n- State: %s\n", author, headRef, baseRef, info.State)
	body := strings.TrimSpace(info.Body)
	if body == "" {
		body = "_(no description)_"
	}
	fmt.Fprintf(&b, "\n## Description\n\n%s\n", body)
	fmt.Fprintf(&b, "\n## Discussion (%d comment(s))\n", len(comments))
	for _, c := range comments {
		if t := strings.TrimSpace(c.Body); t != "" {
			poster := ""
			if c.Poster != nil {
				poster = c.Poster.UserName
			}
			fmt.Fprintf(&b, "\n**%s**: %s\n", poster, t)
		}
	}
	fmt.Fprintf(&b, "\n## Reviews (%d)\n", len(reviews))
	for _, r := range reviews {
		reviewer := ""
		if r.Reviewer != nil {
			reviewer = r.Reviewer.UserName
		}
		fmt.Fprintf(&b, "\n**%s** [%s]: %s\n", reviewer, r.State, strings.TrimSpace(r.Body))
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
