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
	MainChannel       string
	CcrepChannel      string
	OperatorEmail     string
	OperatorName      string
	ForgeURL          string
	IntegrationBranch string
}

// fillDefaults populates empty fields in TemplateData with their generic placeholder text.
func fillDefaults(data TemplateData) TemplateData {
	if data.Actor == "" {
		data.Actor = "<actor>"
	}
	if data.Fam == "" {
		data.Fam = "<fam>"
	}
	if data.MainChannel == "" {
		data.MainChannel = "<main-channel>"
	}
	if data.CcrepChannel == "" {
		data.CcrepChannel = "<ccrep-channel>"
	}
	if data.OperatorEmail == "" {
		data.OperatorEmail = "<operator-email>"
	}
	if data.OperatorName == "" {
		data.OperatorName = "<operator-name>"
	}
	if data.ForgeURL == "" {
		data.ForgeURL = "<forge-url>"
	}
	if data.IntegrationBranch == "" {
		data.IntegrationBranch = "<integration-branch>"
	}
	return data
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

// Read returns the rendered content of the specified doc slug using default placeholder values.
func Read(slug string) ([]byte, error) {
	return Render(slug, fillDefaults(TemplateData{}))
}
