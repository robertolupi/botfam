package fam

import (
	"regexp"
	"testing"
)

func TestGetVersionCompiled(t *testing.T) {
	oldBuildSHA := BuildSHA
	defer func() { BuildSHA = oldBuildSHA }()

	BuildSHA = "test-sha-12345"
	ver := GetVersion()
	if ver != "test-sha-12345" {
		t.Errorf("expected GetVersion to return compiled BuildSHA, got %q", ver)
	}
}

func TestGetVersionFallback(t *testing.T) {
	oldBuildSHA := BuildSHA
	defer func() { BuildSHA = oldBuildSHA }()

	BuildSHA = "dev"
	ver := GetVersion()
	if ver == "" {
		t.Error("expected GetVersion to not be empty")
	}

	// It should either be a 40-character hex string (valid git SHA) or "dev" (if git command fails in test env)
	isHex := regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(ver)
	if !isHex && ver != "dev" {
		t.Errorf("expected GetVersion to return a 40-character hex string or 'dev', got %q", ver)
	}
}
