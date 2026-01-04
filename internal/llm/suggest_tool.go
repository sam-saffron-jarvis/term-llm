package llm

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/samsaffron/term-llm/internal/prompt"
	"google.golang.org/genai"
)

const suggestCommandsToolName = "suggest_commands"

const (
	suggestCommandsDescription          = "Suggest shell commands based on user input"
	suggestCommandsDescriptionWithSearch = "Suggest shell commands based on user input. Call this after gathering any needed information from web search."
)

func suggestToolDescription(enableSearch bool) string {
	if enableSearch {
		return suggestCommandsDescriptionWithSearch
	}
	return suggestCommandsDescription
}

func suggestSchema(numSuggestions int) map[string]interface{} {
	return prompt.SuggestSchema(numSuggestions)
}

func anthropicSuggestTool(numSuggestions int) anthropic.ToolUnionParam {
	schema := suggestSchema(numSuggestions)
	inputSchema := anthropic.ToolInputSchemaParam{
		Type:       constant.Object(schema["type"].(string)),
		Properties: schema["properties"].(map[string]interface{}),
		Required:   requiredFields(schema),
	}

	tool := anthropic.ToolUnionParamOfTool(inputSchema, suggestCommandsToolName)
	tool.OfTool.Description = anthropic.String(suggestToolDescription(false))
	return tool
}

func anthropicSuggestToolBeta(numSuggestions int) anthropic.BetaToolUnionParam {
	schema := suggestSchema(numSuggestions)
	inputSchema := anthropic.BetaToolInputSchemaParam{
		Type:       constant.Object(schema["type"].(string)),
		Properties: schema["properties"].(map[string]interface{}),
		Required:   requiredFields(schema),
	}

	return anthropic.BetaToolUnionParam{
		OfTool: &anthropic.BetaToolParam{
			Name:        suggestCommandsToolName,
			Description: anthropic.String(suggestToolDescription(true)),
			InputSchema: inputSchema,
		},
	}
}

func geminiSuggestTool(numSuggestions int) *genai.Tool {
	return &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        suggestCommandsToolName,
				Description: suggestToolDescription(false),
				Parameters:  schemaToGenai(suggestSchema(numSuggestions)),
			},
		},
	}
}
