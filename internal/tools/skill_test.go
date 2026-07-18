package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/skills"
)

func TestActivateSkillToolEnforcesModelInvocationAndAllowlistPresence(t *testing.T) {
	registry := toolTestSkillRegistry(t, map[string]string{
		"manual": `---
name: manual
description: Manual only
disable-model-invocation: true
---
Manual body
`,
		"empty": `---
name: empty
description: Explicit empty allowlist
allowed-tools: []
---
Empty body
`,
		"unrestricted": `---
name: unrestricted
description: No allowlist
---
Use $ARGUMENTS[0]
`,
	})
	tool := NewActivateSkillTool(registry, nil)

	var callbackCount int
	var callbackPresent bool
	var callbackTools []string
	tool.SetOnActivated(func(allowed []string, present bool) {
		callbackCount++
		callbackPresent = present
		callbackTools = append([]string(nil), allowed...)
	})

	output, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"manual"}`))
	if err != nil {
		t.Fatalf("Execute(manual) error = %v", err)
	}
	if !strings.Contains(output.Content, "PERMISSION_DENIED") || !strings.Contains(output.Content, "disable-model-invocation") {
		t.Fatalf("Execute(manual) output = %q, want permission denial", output.Content)
	}
	if callbackCount != 0 {
		t.Fatalf("manual-only activation invoked callback %d times", callbackCount)
	}

	output, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"empty"}`))
	if err != nil {
		t.Fatalf("Execute(empty) error = %v", err)
	}
	if callbackCount != 1 || !callbackPresent || len(callbackTools) != 0 {
		t.Fatalf("empty callback = count %d, present %v, tools %#v", callbackCount, callbackPresent, callbackTools)
	}

	output, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"unrestricted","prompt":"quoted"}`))
	if err != nil {
		t.Fatalf("Execute(unrestricted) error = %v", err)
	}
	if callbackCount != 2 || callbackPresent || len(callbackTools) != 0 {
		t.Fatalf("unrestricted callback = count %d, present %v, tools %#v", callbackCount, callbackPresent, callbackTools)
	}
	if !strings.Contains(output.Content, "Use $ARGUMENTS[0]") || !strings.Contains(output.Content, "**Task context:** quoted") {
		t.Fatalf("Execute(unrestricted) output = %q, want original body and task context", output.Content)
	}
	if strings.Contains(output.Content, "Invocation arguments") {
		t.Fatalf("Execute(unrestricted) treated model prompt as slash arguments: %q", output.Content)
	}
}

func TestSearchSkillsToolHidesManualOnlySkills(t *testing.T) {
	registry := toolTestSkillRegistry(t, map[string]string{
		"manual-review": `---
name: manual-review
description: Manual review workflow
disable-model-invocation: true
---
Body
`,
		"model-review": `---
name: model-review
description: Model review workflow
user-invocable: false
---
Body
`,
	})
	tool := NewSearchSkillsTool(registry)
	output, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"review"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(output.Content, "manual-review") {
		t.Fatalf("search output revealed manual-only skill: %q", output.Content)
	}
	if !strings.Contains(output.Content, "model-review") {
		t.Fatalf("search output omitted model-invocable skill: %q", output.Content)
	}
}

func toolTestSkillRegistry(t *testing.T, manifests map[string]string) *skills.Registry {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	for name, manifest := range manifests {
		dir := filepath.Join(root, ".skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := skills.NewRegistry(skills.RegistryConfig{
		AutoInvoke:            true,
		IncludeProjectSkills:  true,
		IncludeEcosystemPaths: false,
		ProjectDir:            root,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
