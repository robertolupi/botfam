package fam

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
	"github.com/robertolupi/botfam/internal/forge"
)

const externalReviewHelp = `Usage:
  botfam external-review [opts] MATERIAL [MATERIAL...]
  botfam external-review --pr <index> [opts]

Fan a canonical review prompt + material across one or more LLMs, saving each
raw review out-of-repo under ${BOTFAM_REVIEW_DIR:-~/.botfam/reviews}/<ts>-<slug>;
then spawn a consolidation subagent (do NOT read the raw reviews into context).

Models talk to providers over the OpenAI-compatible chat API (one client for
all). Pick models via repeatable flags (no baked-in defaults):
  --ollama MODEL    local ollama (OLLAMA_HOST, default http://localhost:11434)
  --openai MODEL    OpenAI         (needs OPENAI_API_KEY)
  --gemini MODEL    Gemini         (needs GEMINI_API_KEY)

  --pr <index>   synthesize the material from a Gitea PR (metadata, description,
                 discussion, reviews, unified diff) via the forge; slug pr-<index>.
  --prompt FILE  canonical prompt (default doc/review/EXTERNAL-REVIEW-PROMPT.md);
                 only text below the "PROMPT BEGINS BELOW THIS LINE" marker is used.
  --out DIR      output dir.

Keys are read from the environment only and never printed. Unreachable ollama or
an unset API key is skipped with a warning, not a hard failure.
`

type erProvider struct {
	name    string   // ollama | openai | gemini
	models  []string // selected models for this provider
	baseURL string
	keyEnv  string // env var holding the API key ("" = none, e.g. ollama)
}

// ExternalReviewCmd handles "botfam external-review" (issue #39). It supersedes
// the old tools/external-review.sh.
func ExternalReviewCmd(args []string, out io.Writer) error {
	promptFile := "doc/review/EXTERNAL-REVIEW-PROMPT.md"
	outDir := ""
	pr := ""
	ollamaHost := os.Getenv("OLLAMA_HOST")
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	var ollama, openaiM, gemini []string
	var materials []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s requires a value", a)
			}
			return args[i], nil
		}
		switch {
		case a == "-h" || a == "--help" || a == "help":
			fmt.Fprint(out, externalReviewHelp)
			return nil
		case a == "--pr":
			v, err := need()
			if err != nil {
				return err
			}
			pr = v
		case a == "--ollama":
			v, err := need()
			if err != nil {
				return err
			}
			ollama = append(ollama, v)
		case a == "--openai":
			v, err := need()
			if err != nil {
				return err
			}
			openaiM = append(openaiM, v)
		case a == "--gemini":
			v, err := need()
			if err != nil {
				return err
			}
			gemini = append(gemini, v)
		case a == "--prompt":
			v, err := need()
			if err != nil {
				return err
			}
			promptFile = v
		case a == "--out":
			v, err := need()
			if err != nil {
				return err
			}
			outDir = v
		case a == "--ollama-host":
			v, err := need()
			if err != nil {
				return err
			}
			ollamaHost = v
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown option %q", a)
		default:
			materials = append(materials, a)
		}
	}

	if len(materials) == 0 && pr == "" {
		return fmt.Errorf("no material file(s) and no --pr <index> (see --help)")
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
	} else {
		slug = slugify(strings.TrimSuffix(filepath.Base(materials[0]), filepath.Ext(materials[0])))
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
	if pr != "" {
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
