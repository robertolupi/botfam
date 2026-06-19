package metareview

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateSuggestions(t *testing.T) {
	in := []Suggestion{
		{Label: LabelPhaseInversion, Evidence: "references `x.go`", Confidence: "high"},
		{Label: "risk/made-up", Evidence: "bogus", Confidence: "high"},             // unknown label dropped
		{Label: LabelSuperseded, Evidence: "   ", Confidence: "high"},              // blank evidence dropped
		{Label: LabelSpeculative, Evidence: "missing data", Confidence: "meh"},     // bad confidence -> low
		{Label: LabelHollowValidation, Evidence: "foo_test.go:42", Confidence: ""}, // blank confidence -> low
	}
	got := ValidateSuggestions(in)
	if len(got) != 3 {
		t.Fatalf("want 3 kept, got %d: %+v", len(got), got)
	}
	if got[0].Confidence != "high" {
		t.Errorf("first should stay high: %+v", got[0])
	}
	for _, s := range got[1:] {
		if s.Confidence != "low" {
			t.Errorf("non-high confidence should normalize to low: %+v", s)
		}
	}
}

func TestParseSuggestions(t *testing.T) {
	cases := map[string]struct {
		in      string
		wantLen int
		wantErr bool
	}{
		"clean":             {`{"suggestions":[{"label":"risk/superseded","evidence":"e","confidence":"high"}]}`, 1, false},
		"fenced prose wrap": {"Sure!\n```json\n{\"suggestions\":[]}\n```\ndone", 0, false},
		"empty":             {`{"suggestions":[]}`, 0, false},
		"garbage":           {"not json at all", 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseSuggestions(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("len: got %d want %d", len(got), tc.wantLen)
			}
		})
	}
}

// roundTripFunc adapts a func to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestOllamaClassifier_RequestShape verifies the classifier sends the
// reproducibility params (temperature 0, the seed) and a json_schema response
// format, and parses the chat completion envelope.
func TestOllamaClassifier_RequestShape(t *testing.T) {
	var sentBody map[string]any
	oc := &OllamaClassifier{
		BaseURL: "http://ollama.test/v1",
		Model:   "qwen2.5-coder",
		Seed:    42,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			if req.Body != nil {
				body, _ := io.ReadAll(req.Body)
				_ = json.Unmarshal(body, &sentBody)
			}
			rec.Header().Set("Content-Type", "application/json")
			_, _ = rec.WriteString(`{"choices":[{"message":{"content":"{\"suggestions\":[{\"label\":\"risk/phase-inversion\",\"evidence\":\"references x.go\",\"confidence\":\"high\"}]}"}}]}`)
			return rec.Result(), nil
		})},
	}
	got, err := oc.Classify(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(got) != 1 || got[0].Label != LabelPhaseInversion {
		t.Fatalf("parsed: %+v", got)
	}
	if sentBody["temperature"] != float64(0) {
		t.Errorf("temperature should be 0, got %v", sentBody["temperature"])
	}
	if sentBody["seed"] != float64(42) {
		t.Errorf("seed should be 42, got %v", sentBody["seed"])
	}
	rf, ok := sentBody["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_schema" {
		t.Errorf("response_format should be json_schema, got %v", sentBody["response_format"])
	}
}
