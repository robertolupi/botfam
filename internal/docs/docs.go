package docs

import "embed"

//go:embed all:corpus/*
var Files embed.FS

// Read returns the content of the specified doc slug.
func Read(slug string) ([]byte, error) {
	return Files.ReadFile("corpus/" + slug + ".md")
}
