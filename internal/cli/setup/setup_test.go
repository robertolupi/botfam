package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestRegisterMCPServerGlobally(t *testing.T) {
	tempDir := t.TempDir()

	// Create mock home folders
	geminiDir := filepath.Join(tempDir, ".gemini", "antigravity")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatal(err)
	}
	codexDir := filepath.Join(tempDir, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create mock config files
	mcpConfigPath := filepath.Join(geminiDir, "mcp_config.json")
	codexConfigPath := filepath.Join(codexDir, "config.toml")

	// Initial JSON config content with custom fields to verify map merging
	initialJSONConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"collab": map[string]interface{}{
				"command": "legacy-collab",
			},
			"other": map[string]interface{}{
				"command": "other-server",
			},
			"botfam": map[string]interface{}{
				"command": "old-botfam",
				"cwd":     "custom-gemini-cwd",
			},
			"forge": map[string]interface{}{
				"command":             "old-forge",
				"startup_timeout_sec": 120.0,
			},
		},
	}
	jsonData, err := json.Marshal(initialJSONConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpConfigPath, jsonData, 0644); err != nil {
		t.Fatal(err)
	}

	// Initial TOML config content using snake_case mcp_servers
	initialTOMLConfig := map[string]interface{}{
		"mcp_servers": map[string]interface{}{
			"collab": map[string]interface{}{
				"command": "legacy-collab",
			},
			"other": map[string]interface{}{
				"command": "other-server",
			},
			"botfam": map[string]interface{}{
				"command": "old-botfam",
				"cwd":     "custom-codex-cwd",
			},
			"forge": map[string]interface{}{
				"command":             "old-forge",
				"startup_timeout_sec": 120.0,
			},
		},
	}
	tomlData, err := toml.Marshal(initialTOMLConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexConfigPath, tomlData, 0644); err != nil {
		t.Fatal(err)
	}

	// Set HOME env variable to our tempDir so os.UserHomeDir() resolves to it
	t.Setenv("HOME", tempDir)

	var out bytes.Buffer
	forgeURL := "http://gitea.home.rlupi.com:3000"

	err = RegisterMCPServerGlobally(forgeURL, "botfam", &out)
	if err != nil {
		t.Fatalf("RegisterMCPServerGlobally failed: %v", err)
	}

	// Verify mcp_config.json (antigravity, JSON, camelCase)
	verifyJSONConfig(t, mcpConfigPath)

	// Verify config.toml (codex, TOML, snake_case)
	verifyTOMLConfig(t, codexConfigPath)
}

func TestRegisterMCPServerGloballyEmptyForgeURL(t *testing.T) {
	tempDir := t.TempDir()

	geminiDir := filepath.Join(tempDir, ".gemini", "antigravity")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatal(err)
	}

	mcpConfigPath := filepath.Join(geminiDir, "mcp_config.json")

	// Initial config content containing an existing forge config
	initialConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"forge": map[string]interface{}{
				"command": "stale-forge",
			},
		},
	}
	data, err := json.Marshal(initialConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpConfigPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", tempDir)

	var out bytes.Buffer
	err = RegisterMCPServerGlobally("", "botfam", &out)
	if err != nil {
		t.Fatalf("RegisterMCPServerGlobally failed: %v", err)
	}

	// Read and verify
	data, err = os.ReadFile(mcpConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		McpServers map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}

	// Forge is now served in-process by botfam (#429), so the standalone entry
	// is retired regardless of forgeURL; botfam stays registered.
	if _, ok := config.McpServers["forge"]; ok {
		t.Error("legacy standalone forge server should have been removed (#429)")
	}
	if _, ok := config.McpServers["botfam"]; !ok {
		t.Error("botfam server should still be registered")
	}
}

func TestRegisterMCPServerGloballyWithCustomSlug(t *testing.T) {
	tempDir := t.TempDir()
	geminiDir := filepath.Join(tempDir, ".gemini", "antigravity")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatal(err)
	}
	mcpConfigPath := filepath.Join(geminiDir, "mcp_config.json")
	t.Setenv("HOME", tempDir)

	var out bytes.Buffer
	err := RegisterMCPServerGlobally("http://myforge", "deep-cuts", &out)
	if err != nil {
		t.Fatalf("RegisterMCPServerGlobally failed: %v", err)
	}

	data, err := os.ReadFile(mcpConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		McpServers map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}

	// Forge is retired (#429): neither the slug-scoped nor the generic forge
	// server is registered; only botfam is.
	if _, ok := config.McpServers["forge-deep-cuts"]; ok {
		t.Error("standalone forge-deep-cuts server should not be registered (#429)")
	}
	if _, ok := config.McpServers["forge"]; ok {
		t.Error("generic forge server should not be registered")
	}
	if _, ok := config.McpServers["botfam"]; !ok {
		t.Error("botfam server should be registered")
	}
}

func verifyJSONConfig(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var config struct {
		McpServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	mcpServers := config.McpServers

	// Check botfam server
	botfam, ok := mcpServers["botfam"]
	if !ok {
		t.Fatalf("botfam server missing in %s", path)
	}
	execPath, _ := os.Executable()
	execPath, _ = filepath.Abs(execPath)
	if botfam["command"] != execPath {
		t.Errorf("botfam command = %q, want %q", botfam["command"], execPath)
	}
	if botfam["cwd"] != "custom-gemini-cwd" {
		t.Errorf("expected botfam's custom cwd field 'custom-gemini-cwd' to be preserved, but got %q", botfam["cwd"])
	}

	// Check collab is dropped
	if _, ok := mcpServers["collab"]; ok {
		t.Errorf("collab server was not dropped in %s", path)
	}

	// Check other is preserved
	if _, ok := mcpServers["other"]; !ok {
		t.Errorf("other server was deleted in %s", path)
	}

	// Forge is retired — the standalone entry must be migrated away (#429).
	if _, ok := mcpServers["forge"]; ok {
		t.Errorf("legacy forge server should have been removed in %s", path)
	}
}

func verifyTOMLConfig(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var config struct {
		McpServers map[string]map[string]interface{} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatalf("failed to parse config: %v", err)
	}

	mcpServers := config.McpServers

	// Check botfam server
	botfam, ok := mcpServers["botfam"]
	if !ok {
		t.Fatalf("botfam server missing in %s", path)
	}
	execPath, _ := os.Executable()
	execPath, _ = filepath.Abs(execPath)
	if botfam["command"] != execPath {
		t.Errorf("botfam command = %q, want %q", botfam["command"], execPath)
	}
	if botfam["cwd"] != "custom-codex-cwd" {
		t.Errorf("expected botfam's custom cwd field 'custom-codex-cwd' to be preserved, but got %q", botfam["cwd"])
	}

	// Check collab is dropped
	if _, ok := mcpServers["collab"]; ok {
		t.Errorf("collab server was not dropped in %s", path)
	}

	// Check other is preserved
	if _, ok := mcpServers["other"]; !ok {
		t.Errorf("other server was deleted in %s", path)
	}

	// Forge is retired — the standalone entry must be migrated away (#429).
	if _, ok := mcpServers["forge"]; ok {
		t.Errorf("legacy forge server should have been removed in %s", path)
	}
}
