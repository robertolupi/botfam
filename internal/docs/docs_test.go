package docs

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
)

func TestReadEmbeddedDocs(t *testing.T) {
	slugs := []string{"start", "protocol", "bootstrap", "ops", "operator", "review", "worktrees", "markdown"}
	for _, slug := range slugs {
		content, err := Render(slug, TemplateData{})
		if err != nil {
			t.Errorf("failed to read embedded doc %q: %v", slug, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("embedded doc %q is empty", slug)
		}
		if !bytes.HasPrefix(content, []byte("# ")) {
			t.Errorf("embedded doc %q should start with a header h1, got prefix %q", slug, string(content[:10]))
		}
	}
}

func TestEmbeddedCorpusIsGeneric(t *testing.T) {
	// Grep all files under corpus/ and assert they contain no fam-specific or toolchain-specific strings
	forbidden := []string{
		"#botfam",
		"gitea:3000",
		"token-botfam",
		"claude-bot",
		"agy-bot",
		"wt-agy",
		"wt-claude",
		"wt-rlupi",
		"robertolupi",
		"roberto.lupi",
		"home.rlupi.com",
		"go test",
		"go build",
		"gofmt",
		"go vet",
		"mdformat",
	}

	err := fs.WalkDir(Files, "corpus", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := Files.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read %s: %v", path, err)
			return nil
		}

		contentStr := strings.ToLower(string(content))
		for _, f := range forbidden {
			if strings.Contains(contentStr, strings.ToLower(f)) {
				t.Errorf("file %s contains forbidden/specific string %q", path, f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk corpus: %v", err)
	}
}

func TestTemplateRendering(t *testing.T) {
	data := TemplateData{
		Actor:             "testactor",
		Fam:               "testfam",
		MainChannel:       "#testmain",
		CcrepChannel:      "#testccrep",
		OperatorEmail:     "operator@test.com",
		OperatorName:      "testoperator",
		ForgeURL:          "http://testforge:3000",
		IntegrationBranch: "testbranch",
	}

	slugs := []string{"start", "protocol", "bootstrap", "ops", "operator", "review", "worktrees", "markdown"}
	for _, slug := range slugs {
		content, err := Render(slug, data)
		if err != nil {
			t.Errorf("failed to render embedded doc %q: %v", slug, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("rendered doc %q is empty", slug)
		}

		contentStr := string(content)
		// Check that the template syntax tags like {{ or }} are not present in the rendered output
		if strings.Contains(contentStr, "{{") || strings.Contains(contentStr, "}}") {
			t.Errorf("rendered doc %q contains unrendered template tags:\n%s", slug, contentStr)
		}

		// Verify that specific fuzzed values actually exist in the output where expected
		switch slug {
		case "start":
			if !strings.Contains(contentStr, "testactor") || !strings.Contains(contentStr, "testfam") {
				t.Errorf("rendered start.md missing expected placeholders: %s", contentStr)
			}
		case "worktrees":
			if !strings.Contains(contentStr, "testactor") || !strings.Contains(contentStr, "operator@test.com") {
				t.Errorf("rendered worktrees.md missing expected placeholders: %s", contentStr)
			}
		case "ops":
			if !strings.Contains(contentStr, "testfam") || !strings.Contains(contentStr, "testactor") {
				t.Errorf("rendered ops.md missing expected placeholders: %s", contentStr)
			}
		case "operator":
			if !strings.Contains(contentStr, "testactor") {
				t.Errorf("rendered operator.md missing expected placeholders: %s", contentStr)
			}
		case "protocol":
			if !strings.Contains(contentStr, "testactor") || !strings.Contains(contentStr, "#testmain") || !strings.Contains(contentStr, "testbranch") {
				t.Errorf("rendered protocol.md missing expected placeholders: %s", contentStr)
			}
		case "bootstrap":
			if !strings.Contains(contentStr, "testfam") {
				t.Errorf("rendered bootstrap.md missing expected placeholders: %s", contentStr)
			}
		}
	}
}
