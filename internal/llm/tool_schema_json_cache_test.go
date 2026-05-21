package llm

import "testing"

type countingSchemaValue struct {
	count *int
}

func (v countingSchemaValue) MarshalJSON() ([]byte, error) {
	*v.count = *v.count + 1
	return []byte(`"counted"`), nil
}

func TestToolSchemaJSONCacheReusesImmutableSchema(t *testing.T) {
	marshalCount := 0
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"x-count":    countingSchemaValue{count: &marshalCount},
	}
	specs := []ToolSpec{{Name: "cached", Description: "cached schema", Schema: schema}}

	compatTools, err := buildCompatTools(specs)
	if err != nil {
		t.Fatalf("buildCompatTools first call: %v", err)
	}
	if len(compatTools) != 1 || len(compatTools[0].Function.Parameters) == 0 {
		t.Fatalf("buildCompatTools returned empty parameters: %#v", compatTools)
	}

	compatTools, err = buildCompatTools(specs)
	if err != nil {
		t.Fatalf("buildCompatTools second call: %v", err)
	}
	if len(compatTools) != 1 || len(compatTools[0].Function.Parameters) == 0 {
		t.Fatalf("buildCompatTools returned empty cached parameters: %#v", compatTools)
	}

	ollamaTools, err := buildOllamaTools(specs)
	if err != nil {
		t.Fatalf("buildOllamaTools: %v", err)
	}
	if len(ollamaTools) != 1 || len(ollamaTools[0].Function.Parameters) == 0 {
		t.Fatalf("buildOllamaTools returned empty cached parameters: %#v", ollamaTools)
	}

	if marshalCount != 1 {
		t.Fatalf("schema marshaled %d times, want 1", marshalCount)
	}
}
