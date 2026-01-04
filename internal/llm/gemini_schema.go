package llm

import "google.golang.org/genai"

func schemaToGenai(schema map[string]interface{}) *genai.Schema {
	if schema == nil {
		return &genai.Schema{Type: genai.TypeString}
	}

	genSchema := &genai.Schema{
		Type:        schemaTypeFromValue(schema),
		Description: stringField(schema, "description"),
		Required:    requiredFields(schema),
	}

	if props, ok := schema["properties"].(map[string]interface{}); ok {
		genSchema.Properties = make(map[string]*genai.Schema, len(props))
		for name, prop := range props {
			if propMap, ok := prop.(map[string]interface{}); ok {
				genSchema.Properties[name] = schemaToGenai(propMap)
			}
		}
	}

	if items, ok := schema["items"].(map[string]interface{}); ok {
		genSchema.Items = schemaToGenai(items)
	}

	return genSchema
}

func schemaTypeFromValue(schema map[string]interface{}) genai.Type {
	if t, ok := schema["type"].(string); ok {
		switch t {
		case "string":
			return genai.TypeString
		case "integer":
			return genai.TypeInteger
		case "number":
			return genai.TypeNumber
		case "boolean":
			return genai.TypeBoolean
		case "array":
			return genai.TypeArray
		case "object":
			return genai.TypeObject
		}
	}
	return genai.TypeString
}

func requiredFields(schema map[string]interface{}) []string {
	if required, ok := schema["required"].([]string); ok {
		return required
	}
	if required, ok := schema["required"].([]interface{}); ok {
		result := make([]string, 0, len(required))
		for _, r := range required {
			if s, ok := r.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func stringField(schema map[string]interface{}, key string) string {
	if v, ok := schema[key].(string); ok {
		return v
	}
	return ""
}
