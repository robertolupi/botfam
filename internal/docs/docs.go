package docs

import (
	"bytes"
	"embed"
	"text/template"
)

//go:embed all:corpus/*
var Files embed.FS

// TemplateData holds the runtime context variables to interpolate into the generic documents.
type TemplateData struct {
	Actor             string
	Fam               string
	OperatorEmail     string
	OperatorName      string
	ForgeURL          string
	IntegrationBranch string
	ReleaseBranch     string
}

// Render returns the content of the specified doc slug, parsed and executed as a Go text/template with the provided TemplateData.
func Render(slug string, data TemplateData) ([]byte, error) {
	content, err := Files.ReadFile("corpus/" + slug + ".md")
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New(slug).Parse(string(content))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
