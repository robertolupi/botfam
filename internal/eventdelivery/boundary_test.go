package eventdelivery_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCorePackagesDoNotDependOnSingleHostBinding(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./internal/eventdelivery/...")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var pkg struct {
			ImportPath string
			Dir        string
			GoFiles    []string
			Imports    []string
		}
		if err := dec.Decode(&pkg); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(pkg.ImportPath, "/singlehost") {
			continue
		}
		for _, imp := range pkg.Imports {
			if strings.HasSuffix(imp, "/internal/eventdelivery/singlehost") {
				t.Fatalf("%s imports singlehost binding package", pkg.ImportPath)
			}
		}
		for _, file := range pkg.GoFiles {
			contents, err := os.ReadFile(filepath.Join(pkg.Dir, file))
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{"flock", "pid", "PID", ".sock", "unix", "127.0.0.1", "loopback"} {
				if bytes.Contains(contents, []byte(token)) {
					t.Fatalf("%s names single-host binding token %q", filepath.Join(pkg.Dir, file), token)
				}
			}
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
