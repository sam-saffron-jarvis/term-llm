package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWithLockedRecentFile_SerializesConcurrentAccess(t *testing.T) {
	recentPath := filepath.Join(t.TempDir(), "memory", "recent.md")

	firstAcquired := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondAcquired := make(chan struct{})
	errCh := make(chan error, 2)

	go func() {
		errCh <- withLockedRecentFile(recentPath, func() error {
			close(firstAcquired)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("first lock was not acquired")
	}

	go func() {
		errCh <- withLockedRecentFile(recentPath, func() error {
			close(secondAcquired)
			return nil
		})
	}()

	select {
	case <-secondAcquired:
		t.Fatal("second lock acquired before first lock released")
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseFirst)

	select {
	case <-secondAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock was not acquired after first lock released")
	}

	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatalf("withLockedRecentFile: %v", err)
		}
	}
}

func TestWriteRecentFileAtomically_ReplacesContentWithoutLeakingTemps(t *testing.T) {
	dir := t.TempDir()
	recentPath := filepath.Join(dir, "recent.md")
	if err := os.WriteFile(recentPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed recent.md: %v", err)
	}

	if err := writeRecentFileAtomically(recentPath, "new content"); err != nil {
		t.Fatalf("writeRecentFileAtomically: %v", err)
	}

	data, err := os.ReadFile(recentPath)
	if err != nil {
		t.Fatalf("read recent.md: %v", err)
	}
	if string(data) != "new content" {
		t.Fatalf("got %q, want %q", string(data), "new content")
	}

	info, err := os.Stat(recentPath)
	if err != nil {
		t.Fatalf("stat recent.md: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %o, want 644", got)
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, ".recent.md.*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("unexpected temp files left behind: %v", leftovers)
	}
}
