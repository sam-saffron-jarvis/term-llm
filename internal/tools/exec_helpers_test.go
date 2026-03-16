package tools

import (
	"os"
	"strings"
	"testing"
)

func TestExpandTilde_TildeSlash(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	got := expandTilde("~/foo/bar")
	want := home + "/foo/bar"
	if got != want {
		t.Errorf("expandTilde(~/foo/bar) = %q, want %q", got, want)
	}
}

func TestExpandTilde_BearTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	got := expandTilde("~")
	if got != home {
		t.Errorf("expandTilde(~) = %q, want %q", got, home)
	}
}

func TestExpandTilde_AbsPath(t *testing.T) {
	got := expandTilde("/absolute/path")
	if got != "/absolute/path" {
		t.Errorf("expandTilde(/absolute/path) = %q, want unchanged", got)
	}
}

func TestExpandTilde_RelativePath(t *testing.T) {
	got := expandTilde("relative/path")
	if got != "relative/path" {
		t.Errorf("expandTilde(relative/path) = %q, want unchanged", got)
	}
}

func TestExpandTilde_TildeInMiddle(t *testing.T) {
	// ~ not at start should be left alone
	got := expandTilde("/some/~/path")
	if got != "/some/~/path" {
		t.Errorf("expandTilde with mid-path tilde = %q, want unchanged", got)
	}
}

func TestResolveToolPath_TildeExpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	// resolveToolPath should not return "file not found" for ~/
	// We just check it expands correctly — home dir itself must exist.
	got, err := resolveToolPath("~", false)
	if err != nil {
		t.Fatalf("resolveToolPath(~) returned error: %v", err)
	}
	if !strings.HasPrefix(got, home) {
		t.Errorf("resolveToolPath(~) = %q, expected prefix %q", got, home)
	}
}
