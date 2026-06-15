package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMintToken(t *testing.T) {
	var gotUser, gotPass, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sha1":"abc123token","name":"botfam"}`))
	}))
	defer srv.Close()

	tok, err := mintToken(srv.URL, "claude-bot", "hunter2", []string{"write:repository", "write:issue"})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	if tok != "abc123token" {
		t.Errorf("token = %q", tok)
	}
	if gotUser != "claude-bot" || gotPass != "hunter2" {
		t.Errorf("basic auth = %q/%q", gotUser, gotPass)
	}
	if gotPath != "/api/v1/users/claude-bot/tokens" {
		t.Errorf("path = %q", gotPath)
	}
	if scopes, _ := gotBody["scopes"].([]any); len(scopes) != 2 {
		t.Errorf("scopes = %v", gotBody["scopes"])
	}
}

func TestMintTokenError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
	}))
	defer srv.Close()
	if _, err := mintToken(srv.URL, "claude-bot", "wrong", nil); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestWriteTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".botfam", "token-claude-code")
	if err := writeTokenFile(path, "secrettoken"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "secrettoken" {
		t.Errorf("token contents = %q", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode = %o, want 600", info.Mode().Perm())
	}
}
