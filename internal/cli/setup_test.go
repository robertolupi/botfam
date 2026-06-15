package cli

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
	verifyJSONConfig(t, mcpConfigPath, tempDir, "antigravity", forgeURL)

	// Verify config.toml (codex, TOML, snake_case)
	verifyTOMLConfig(t, codexConfigPath, tempDir, "codex", forgeURL)
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

	if _, ok := config.McpServers["forge"]; !ok {
		t.Error("forge server should NOT have been deleted when forgeURL is empty (Issue #227)")
	}
	if _, ok := config.McpServers["botfam"]; !ok {
		t.Error("botfam server should still be registered when forgeURL is empty")
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

	if _, ok := config.McpServers["forge-deep-cuts"]; !ok {
		t.Error("forge server should have been registered as forge-deep-cuts for slug deep-cuts")
	}
	if _, ok := config.McpServers["forge"]; ok {
		t.Error("generic forge server should not have been registered when a custom slug is present")
	}
}

func verifyJSONConfig(t *testing.T, path, home, harness, forgeURL string) {
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

	// Check forge server
	forge, ok := mcpServers["forge"]
	if !ok {
		t.Fatalf("forge server missing in %s", path)
	}

	wantCommand := filepath.Join(home, "bin", "gitea-mcp-server")
	if forge["command"] != wantCommand {
		t.Errorf("forge command = %q, want %q", forge["command"], wantCommand)
	}

	if val, ok := forge["startup_timeout_sec"].(float64); !ok || val != 120.0 {
		t.Errorf("expected forge's custom startup_timeout_sec field to be preserved as 120.0, but got %v", forge["startup_timeout_sec"])
	}

	wantTokenFile := filepath.Join(home, ".botfam", "token-"+harness)
	envMap, _ := forge["env"].(map[string]interface{})
	if envMap == nil || envMap["GITEA_ACCESS_TOKEN_FILE"] != wantTokenFile {
		t.Errorf("forge token file = %q, want %q", envMap["GITEA_ACCESS_TOKEN_FILE"], wantTokenFile)
	}
}

func verifyTOMLConfig(t *testing.T, path, home, harness, forgeURL string) {
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

	// Check forge server
	forge, ok := mcpServers["forge"]
	if !ok {
		t.Fatalf("forge server missing in %s", path)
	}

	wantCommand := filepath.Join(home, "bin", "gitea-mcp-server")
	if forge["command"] != wantCommand {
		t.Errorf("forge command = %q, want %q", forge["command"], wantCommand)
	}

	// In TOML, integers or floats can be parsed as int64 or float64. Let's handle both.
	var gotTimeout float64
	switch v := forge["startup_timeout_sec"].(type) {
	case float64:
		gotTimeout = v
	case int64:
		gotTimeout = float64(v)
	}
	if gotTimeout != 120.0 {
		t.Errorf("expected forge's custom startup_timeout_sec field to be preserved as 120.0, but got %v", forge["startup_timeout_sec"])
	}

	wantTokenFile := filepath.Join(home, ".botfam", "token-"+harness)
	envMap, _ := forge["env"].(map[string]interface{})
	if envMap == nil || envMap["GITEA_ACCESS_TOKEN_FILE"] != wantTokenFile {
		t.Errorf("forge token file = %q, want %q", envMap["GITEA_ACCESS_TOKEN_FILE"], wantTokenFile)
	}
}
