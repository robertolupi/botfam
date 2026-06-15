package version

import (
	"regexp"
	"testing"
)

func TestGetVersionCompiled(t *testing.T) {
	oldBuildSHA := BuildSHA
	defer func() { BuildSHA = oldBuildSHA }()

	BuildSHA = "0.1.0 (test-sha-12345, 2026-06-14)"
	ver := GetVersion()
	if ver != "0.1.0 (test-sha-12345, 2026-06-14)" {
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

	// It should either match "0.1.0 ([0-9a-f]{7}(-dirty)?)" or "0.1.0 ([0-9a-f]{7}(-dirty)?, [0-9-]{10})" or "dev"
	isVersionPattern := regexp.MustCompile(`^0\.1\.0 \([0-9a-f]{7}(-dirty)?(, [0-9-]{10})?\)$`).MatchString(ver)
	if !isVersionPattern && ver != "dev" {
		t.Errorf("expected GetVersion to return '0.1.0 (<7-char-hex>)' or 'dev', got %q", ver)
	}
}
