package session

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGetHandoverPathPinnedPerDirAndDate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ResetHandoverPathCache()
	t.Cleanup(ResetHandoverPathCache)

	first, err := GetHandoverPath(".", "2026-07-02")
	if err != nil {
		t.Fatalf("GetHandoverPath: %v", err)
	}
	base := filepath.Base(first)
	if !strings.HasPrefix(base, "2026-07-02-") {
		t.Fatalf("expected date prefix on %q", base)
	}
	if !IsRandomHandoverName(base) {
		t.Fatalf("expected random handover name, got %q", base)
	}

	second, err := GetHandoverPath(".", "2026-07-02")
	if err != nil {
		t.Fatalf("GetHandoverPath: %v", err)
	}
	if second != first {
		t.Fatalf("path not pinned across calls: %q != %q", second, first)
	}

	otherDate, err := GetHandoverPath(".", "2026-07-03")
	if err != nil {
		t.Fatalf("GetHandoverPath: %v", err)
	}
	if otherDate == first {
		t.Fatal("different date should produce a different path")
	}

	otherDir, err := GetHandoverPath(t.TempDir(), "2026-07-02")
	if err != nil {
		t.Fatalf("GetHandoverPath: %v", err)
	}
	if otherDir == first {
		t.Fatal("different project dir should produce a different path")
	}
}
