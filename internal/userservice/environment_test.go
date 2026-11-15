package userservice

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCredentialsDirectoryEnvironment(t *testing.T) {
	const key = "TERM_LLM_CREDENTIALS_DIR"
	if !EnvironmentKey(key) || SecretName(key) {
		t.Fatal("credential directory must be an allowed environment entry, not a secret")
	}
	s := fixtureSpec(t)
	dir := filepath.Join(t.TempDir(), "oauth stores")
	s.Environment[key] = dir
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "service.json")
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	recorded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	env := RunnerEnvironment(recorded, []string{key + "=/unrelated/ambient"}, nil)
	if !slices.Contains(env, key+"="+dir) || slices.Contains(env, key+"=/unrelated/ambient") {
		t.Fatalf("runner did not preserve recorded credential directory: %v", env)
	}
	delete(recorded.Environment, key)
	if slices.Contains(RunnerEnvironment(recorded, []string{key + "=/unrelated/ambient"}, nil), key+"=/unrelated/ambient") {
		t.Fatal("runner inherited unrecorded credential directory")
	}
}
