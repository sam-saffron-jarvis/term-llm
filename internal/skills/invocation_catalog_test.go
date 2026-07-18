package skills

import (
	"reflect"
	"testing"
)

func TestRegistryInvocationCatalogs(t *testing.T) {
	registry := testInvocationRegistry(t, map[string]string{
		"default": `---
name: default
description: Default visibility
---
Body
`,
		"manual-only": `---
name: manual-only
description: User only
disable-model-invocation: true
---
Body
`,
		"model-only": `---
name: model-only
description: Model only
user-invocable: false
---
Body
`,
		"hidden": `---
name: hidden
description: Hidden from both
user-invocable: false
disable-model-invocation: true
---
Body
`,
		"never": `---
name: never
description: Config explicit only
---
Body
`,
		"invalid": `---
name: invalid
description: Invalid client metadata
user-invocable: sometimes
---
Body
`,
	})
	registry.config.AutoInvoke = true
	registry.neverAutoSet["never"] = true

	userSkills, diagnostics, err := registry.ListUserInvocable()
	if err != nil {
		t.Fatalf("ListUserInvocable() error = %v", err)
	}
	if got, want := skillNames(userSkills), []string{"default", "manual-only", "never"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListUserInvocable() = %#v, want %#v", got, want)
	}
	if len(diagnostics) != 1 || diagnostics[0].Name != "invalid" {
		t.Fatalf("ListUserInvocable() diagnostics = %#v, want invalid metadata diagnostic", diagnostics)
	}

	modelSkills, diagnostics, err := registry.ListModelInvocable()
	if err != nil {
		t.Fatalf("ListModelInvocable() error = %v", err)
	}
	if got, want := skillNames(modelSkills), []string{"default", "model-only"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListModelInvocable() = %#v, want %#v", got, want)
	}
	if len(diagnostics) != 1 || diagnostics[0].Name != "invalid" {
		t.Fatalf("ListModelInvocable() diagnostics = %#v, want invalid metadata diagnostic", diagnostics)
	}

	results, err := registry.Search("only", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got, want := skillNames(results), []string{"model-only"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() = %#v, want %#v", got, want)
	}
	results, err = registry.Search("manual", 10)
	if err != nil {
		t.Fatalf("Search(manual) error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search(manual) = %#v, want hidden manual-only skill", skillNames(results))
	}
}

func TestRegistryAutoInvokeFalseOnlyDisablesModelCatalog(t *testing.T) {
	registry := testInvocationRegistry(t, map[string]string{
		"default": `---
name: default
description: Default visibility
---
Body
`,
	})
	registry.config.AutoInvoke = false

	modelSkills, _, err := registry.ListModelInvocable()
	if err != nil {
		t.Fatal(err)
	}
	if len(modelSkills) != 0 {
		t.Fatalf("ListModelInvocable() = %#v, want empty when auto_invoke is false", skillNames(modelSkills))
	}
	userSkills, _, err := registry.ListUserInvocable()
	if err != nil {
		t.Fatal(err)
	}
	if got := skillNames(userSkills); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("ListUserInvocable() = %#v, want explicit user skill", got)
	}
}

func skillNames(skills []*Skill) []string {
	names := make([]string, len(skills))
	for i, skill := range skills {
		names[i] = skill.Name
	}
	return names
}
