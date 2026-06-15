package metareview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/openai/openai-go/v2/shared"
)

// Suggestion is one risk the model is willing to assert, with the evidence it
// chose to cite. Confidence is "high" or "low" (anything else is normalized).
type Suggestion struct {
	Label      string `json:"label"`
	Evidence   string `json:"evidence"`
	Confidence string `json:"confidence"`
}

// Classifier confirms+phrases candidate signals. It is given a fully-built
// prompt (glossary + gathered facts + artifact excerpt) and returns the subset
// of risks it can confirm. Abstracted so the driver and tests can inject a
// deterministic fake instead of a live model.
type Classifier interface {
	Classify(ctx context.Context, prompt string) ([]Suggestion, error)
}

// suggestionSchema is the JSON-schema the model is constrained to. A root
// object (OpenAI structured output requires an object, not a bare array) with a
// single "suggestions" array of {label, evidence, confidence}.
var suggestionSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"suggestions"},
	"properties": map[string]any{
		"suggestions": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"label", "evidence", "confidence"},
				"properties": map[string]any{
					"label":      map[string]any{"type": "string", "enum": PerArtifactLabels},
					"evidence":   map[string]any{"type": "string"},
					"confidence": map[string]any{"type": "string", "enum": []string{"high", "low"}},
				},
			},
		},
	},
}

// OllamaClassifier runs a local model over the OpenAI-compatible chat API
// (mirroring external-review's wiring). Output is JSON-schema-constrained,
// temperature 0 with a fixed seed for reproducibility.
type OllamaClassifier struct {
	BaseURL string // e.g. http://localhost:11434/v1
	APIKey  string // "" for ollama
	Model   string
	Seed    int64

	// HTTPClient overrides the default timeout client; set in tests to serve
	// canned completions without a live model.
	HTTPClient *http.Client
}

// Classify implements Classifier against the local model.
func (o *OllamaClassifier) Classify(ctx context.Context, prompt string) ([]Suggestion, error) {
	key := o.APIKey
	if key == "" {
		key = "none" // ollama ignores it
	}
	httpClient := o.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 300 * time.Second}
	}
	client := openai.NewClient(
		option.WithBaseURL(o.BaseURL),
		option.WithAPIKey(key),
		option.WithHTTPClient(httpClient),
	)
	resp, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(o.Model),
		Temperature: openai.Float(0),
		Seed:        openai.Int(o.Seed),
		Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				// Type elided: marshals its zero value as "json_schema".
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "risk_suggestions",
					Strict: openai.Bool(true),
					Schema: suggestionSchema,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("model returned no choices")
	}
	return parseSuggestions(resp.Choices[0].Message.Content)
}

// parseSuggestions decodes the model's JSON. It tolerates a model that wraps
// the object in prose or markdown fences by extracting the outermost object;
// malformed output yields an error so the driver drops it rather than posts it.
func parseSuggestions(content string) ([]Suggestion, error) {
	raw := extractJSONObject(content)
	if raw == "" {
		return nil, fmt.Errorf("no JSON object in model output")
	}
	var wrapper struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, fmt.Errorf("decode model output: %w", err)
	}
	return wrapper.Suggestions, nil
}

// extractJSONObject returns the substring from the first '{' to the last '}',
// or "" if none. Cheap brace-bracketing — sufficient for our small payloads.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// ValidateSuggestions drops malformed entries: unknown/blank labels, blank
// evidence, and normalizes confidence to "high"/"low" (defaulting unknown to
// "low"). This is the safety gate — advisory output is cheap, so when in doubt
// we drop. Returns a new slice; never mutates the input.
func ValidateSuggestions(in []Suggestion) []Suggestion {
	out := make([]Suggestion, 0, len(in))
	for _, s := range in {
		s.Label = strings.TrimSpace(s.Label)
		s.Evidence = strings.TrimSpace(s.Evidence)
		if !isPerArtifactLabel(s.Label) || s.Evidence == "" {
			continue
		}
		if c := strings.ToLower(strings.TrimSpace(s.Confidence)); c == "high" {
			s.Confidence = "high"
		} else {
			s.Confidence = "low"
		}
		out = append(out, s)
	}
	return out
}
