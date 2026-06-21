package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIngesterFailsFastOnMissingFamRoot: a spoolDir whose fam root (grandparent)
// is absent must error immediately rather than MkdirAll a bogus spool tree the
// reader never watches (#263).
func TestIngesterFailsFastOnMissingFamRoot(t *testing.T) {
	// Grandparent of spoolDir is <tmp>/no-fam-root, which is never created.
	spoolDir := filepath.Join(t.TempDir(), "no-fam-root", "spool", "claude")
	ing := NewIngester(spoolDir, 20*time.Millisecond)
	err := ing.Run(context.Background())
	if err == nil {
		t.Fatal("expected a fail-fast error for a missing fam root")
	}
	if !strings.Contains(err.Error(), "fam root") {
		t.Errorf("error should name the missing fam root, got: %v", err)
	}
	if _, statErr := os.Stat(spoolDir); !os.IsNotExist(statErr) {
		t.Errorf("ingester fabricated a spool tree at %s despite the missing fam root", spoolDir)
	}
}

func TestWriterLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.lock")

	l1, err := acquireWriterLock(context.Background(), path, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	// A second acquisition must block; with a short ctx it fails rather than
	// stealing the lock.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireWriterLock(ctx, path, 10*time.Millisecond); err == nil {
		t.Fatal("second writer acquired the lock while the first held it")
	}

	// After release, it is acquirable again.
	l1.release()
	l2, err := acquireWriterLock(context.Background(), path, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("lock not reacquirable after release: %v", err)
	}
	l2.release()
}
