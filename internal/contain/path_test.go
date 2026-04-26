package contain

import (
	"path/filepath"
	"testing"
)

func TestPathsUseXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	root, err := ContainersRoot()
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(xdg, "term-llm", "containers")
	if root != wantRoot {
		t.Fatalf("ContainersRoot() = %q, want %q", root, wantRoot)
	}

	dir, err := ContainerDir("foo")
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(wantRoot, "foo") {
		t.Fatalf("ContainerDir() = %q", dir)
	}

	compose, err := ComposePath("foo")
	if err != nil {
		t.Fatal(err)
	}
	if compose != filepath.Join(wantRoot, "foo", "compose.yaml") {
		t.Fatalf("ComposePath() = %q", compose)
	}
}
