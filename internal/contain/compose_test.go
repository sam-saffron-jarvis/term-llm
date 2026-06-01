package contain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadComposeInfoHintsAndLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	data := []byte(`x-term-llm:
  default_service: web
  shell: /bin/bash
  preferred_cli: term-llm
  agent: developer
  exec_recipes:
    agent:
      description: Chat with agent
      command:
        - term-llm
        - chat
        - "@developer"
    redis:
      command: redis-cli -h redis
    seed-chatgpt-auth:
      description: Seed ChatGPT credentials
      copy_files:
        - from: "{{config_dir}}/chatgpt_oauth.json"
          to: "{{workspace}}/.config/term-llm/chatgpt_oauth.json"
          mode: "0600"
          missing_hint: Run auth first
      post_run_hint: Restart {{name}}
      command: [true]
services:
  web:
    build:
      context: /tmp/managed-agent
    labels:
      org.term-llm.contain: "true"
      org.term-llm.contain.name: app
      org.term-llm.contain.user: appuser
  worker:
    image: alpine
    labels:
      - org.term-llm.contain.service=worker
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := ReadComposeInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Invalid {
		t.Fatalf("compose invalid: %s", info.InvalidReason)
	}
	if info.DefaultService() != "web" || info.Shell() != "/bin/bash" {
		t.Fatalf("hints = %+v", info.Hints)
	}
	if got := info.Hints.ExecRecipes["agent"].Command; len(got) != 3 || got[0] != "term-llm" || got[2] != "@developer" {
		t.Fatalf("agent recipe command = %#v", got)
	}
	if got := info.Hints.ExecRecipes["redis"].Command; len(got) != 3 || got[0] != "redis-cli" || got[2] != "redis" {
		t.Fatalf("redis recipe command = %#v", got)
	}
	seed := info.Hints.ExecRecipes["seed-chatgpt-auth"]
	if len(seed.CopyFiles) != 1 {
		t.Fatalf("seed copy_files = %#v", seed.CopyFiles)
	}
	if got := seed.CopyFiles[0]; got.From != "{{config_dir}}/chatgpt_oauth.json" || got.To != "{{workspace}}/.config/term-llm/chatgpt_oauth.json" || got.Mode != "0600" || got.MissingHint != "Run auth first" {
		t.Fatalf("seed copy_file = %#v", got)
	}
	if seed.PostRunHint != "Restart {{name}}" {
		t.Fatalf("seed post_run_hint = %q", seed.PostRunHint)
	}
	if got := info.Services["web"].Labels["org.term-llm.contain.name"]; got != "app" {
		t.Fatalf("map label = %q", got)
	}
	if got := info.Services["web"].BuildContext; got != "/tmp/managed-agent" {
		t.Fatalf("build context = %q", got)
	}
	if got := info.DefaultUser("web"); got != "appuser" {
		t.Fatalf("default user = %q", got)
	}
	if got := info.Services["worker"].Labels["org.term-llm.contain.service"]; got != "worker" {
		t.Fatalf("list label = %q", got)
	}
}

func TestReadComposeInfoFallbacksAndInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte("services:\n  api:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := ReadComposeInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.DefaultService() != "app" || info.Shell() != "/bin/sh" {
		t.Fatalf("fallbacks = service %q shell %q", info.DefaultService(), info.Shell())
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("services: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	badInfo, err := ReadComposeInfo(bad)
	if err != nil {
		t.Fatal(err)
	}
	if !badInfo.Invalid || badInfo.InvalidReason == "" {
		t.Fatalf("badInfo = %+v", badInfo)
	}
}
