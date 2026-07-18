package skills

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActivatorActivate(t *testing.T) {
	registry := testInvocationRegistry(t, map[string]string{
		"review": `---
name: review
description: Review changes
argument-hint: "[scope]"
disable-model-invocation: true
allowed-tools: []
tools:
  - name: review_helper
    description: Review helper
    script: scripts/review.sh
---
Review $ARGUMENTS[0]. Raw: $ARGUMENTS
`,
		"model-only": `---
name: model-only
description: Model only
user-invocable: false
---
Model workflow
`,
		"forked": `---
name: forked
description: Forked workflow
context: fork
agent: reviewer
model: fast
allowed-tools: read_file grep
---
Inspect $ARGUMENTS.
`,
	})
	activator := NewActivator(registry)

	activation, err := activator.Activate(ActivationRequest{
		Name:    "review",
		RawArgs: `internal/config "error paths"`,
		Origin:  SkillActivationUser,
	})
	if err != nil {
		t.Fatalf("Activate(user) error = %v", err)
	}
	if activation.Skill.Name != "review" || activation.BaseDir == "" {
		t.Fatalf("Activate(user) skill = %#v, base = %q", activation.Skill, activation.BaseDir)
	}
	if got, want := activation.Prompt, `Review internal/config. Raw: internal/config "error paths"`; got != want {
		t.Fatalf("Activate(user).Prompt = %q, want %q", got, want)
	}
	if !activation.AllowedToolsPresent || len(activation.AllowedTools) != 0 {
		t.Fatalf("Activate(user) allowlist = (%v, %#v), want explicit empty", activation.AllowedToolsPresent, activation.AllowedTools)
	}
	if len(activation.ToolDefs) != 1 || activation.ToolDefs[0].Name != "review_helper" {
		t.Fatalf("Activate(user).ToolDefs = %#v", activation.ToolDefs)
	}
	if activation.Metadata.ArgumentHint != "[scope]" {
		t.Fatalf("Activate(user).Metadata = %#v", activation.Metadata)
	}
	instructions := RenderActivationInstructions(activation)
	for _, want := range []string{"# Skill: review", "**Source:** " + activation.BaseDir, "Review internal/config. Raw: internal/config \"error paths\""} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("RenderActivationInstructions() missing %q: %s", want, instructions)
		}
	}

	_, err = activator.Activate(ActivationRequest{Name: "review", Origin: SkillActivationModel})
	var activationErr *ActivationError
	if !errors.As(err, &activationErr) || activationErr.Kind != ActivationDisabledForOrigin {
		t.Fatalf("Activate(model manual-only) error = %v, want origin-disabled ActivationError", err)
	}

	_, err = activator.Activate(ActivationRequest{Name: "model-only", Origin: SkillActivationUser})
	if !errors.As(err, &activationErr) || activationErr.Kind != ActivationDisabledForOrigin {
		t.Fatalf("Activate(user model-only) error = %v, want origin-disabled ActivationError", err)
	}

	forked, err := activator.Activate(ActivationRequest{Name: "forked", RawArgs: "tree", Origin: SkillActivationUser})
	if err != nil {
		t.Fatalf("Activate(forked) error = %v", err)
	}
	if forked.Metadata.Execution != SkillExecutionIsolatedAgent || forked.Metadata.Agent != "reviewer" || forked.Metadata.Model != "fast" {
		t.Fatalf("Activate(forked).Metadata = %#v", forked.Metadata)
	}
	if !forked.AllowedToolsPresent || !reflect.DeepEqual(forked.AllowedTools, []string{"read_file", "grep"}) {
		t.Fatalf("Activate(forked) allowlist = (%v, %#v)", forked.AllowedToolsPresent, forked.AllowedTools)
	}
}

func TestActivatorTypedErrors(t *testing.T) {
	registry := testInvocationRegistry(t, map[string]string{
		"invalid": `---
name: invalid
description: Invalid metadata
context: sibling
---
Body
`,
		"quotes": `---
name: quotes
description: Quote parser
---
$ARGUMENTS[0]
`,
	})
	activator := NewActivator(registry)

	tests := []struct {
		name string
		req  ActivationRequest
		kind ActivationErrorKind
	}{
		{name: "not found", req: ActivationRequest{Name: "missing", Origin: SkillActivationUser}, kind: ActivationNotFound},
		{name: "bad metadata", req: ActivationRequest{Name: "invalid", Origin: SkillActivationUser}, kind: ActivationInvalidMetadata},
		{name: "bad arguments", req: ActivationRequest{Name: "quotes", RawArgs: "'bad", Origin: SkillActivationUser}, kind: ActivationInvalidArguments},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := activator.Activate(tt.req)
			var activationErr *ActivationError
			if !errors.As(err, &activationErr) || activationErr.Kind != tt.kind {
				t.Fatalf("Activate() error = %v, want kind %v", err, tt.kind)
			}
		})
	}
}

func testInvocationRegistry(t *testing.T, manifests map[string]string) *Registry {
	t.Helper()
	root := t.TempDir()
	for name, manifest := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry(RegistryConfig{IncludeProjectSkills: false, IncludeEcosystemPaths: false})
	if err != nil {
		t.Fatal(err)
	}
	registry.searchPaths = []searchPath{{path: root, source: SourceUser}}
	return registry
}
