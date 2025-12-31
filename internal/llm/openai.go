package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/samsaffron/term-llm/internal/prompt"
)

type OpenAIProvider struct {
	client *openai.Client
	model  string
	apiKey string
}

func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIProvider{
		client: &client,
		model:  model,
		apiKey: apiKey,
	}
}

func (p *OpenAIProvider) SuggestCommands(ctx context.Context, userInput string, shell string, systemContext string, enableSearch bool, debug bool) ([]CommandSuggestion, error) {
	// Define the function tool for structured output
	functionTool := responses.ToolParamOfFunction(
		"suggest_commands",
		prompt.JSONSchema(),
		true,
	)
	functionTool.OfFunction.Description = openai.String("Suggest shell commands based on user input")

	tools := []responses.ToolUnionParam{functionTool}

	// Add web search tool if enabled
	if enableSearch {
		webSearchTool := responses.ToolParamOfWebSearchPreview(responses.WebSearchToolTypeWebSearchPreview)
		tools = append([]responses.ToolUnionParam{webSearchTool}, tools...)
	}

	systemPrompt := prompt.SystemPrompt(shell, systemContext, enableSearch)
	userPrompt := prompt.UserPrompt(userInput)

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: OpenAI Request ===")
		fmt.Fprintf(os.Stderr, "Model: %s\n", p.model)
		if enableSearch {
			fmt.Fprintln(os.Stderr, "Tools: web_search_preview, suggest_commands")
		} else {
			fmt.Fprintln(os.Stderr, "Tools: suggest_commands")
		}
		fmt.Fprintf(os.Stderr, "Instructions:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "Input:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "=============================")
	}

	resp, err := p.client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        shared.ResponsesModel(p.model),
		Instructions: openai.String(systemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(userPrompt),
		},
		Tools: tools,
	})
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: OpenAI Response ===")
		fmt.Fprintf(os.Stderr, "Status: %s\n", resp.Status)
		for i, item := range resp.Output {
			fmt.Fprintf(os.Stderr, "Output %d: type=%s\n", i, item.Type)
			switch item.Type {
			case "function_call":
				fmt.Fprintf(os.Stderr, "  Function: %s\n", item.Name)
				fmt.Fprintf(os.Stderr, "  Arguments:\n%s\n", item.Arguments)
			case "message":
				for _, content := range item.Content {
					fmt.Fprintf(os.Stderr, "  Content type: %s\n", content.Type)
					if content.Text != "" {
						fmt.Fprintf(os.Stderr, "  Text: %s\n", content.Text)
					}
				}
			case "web_search_call":
				fmt.Fprintf(os.Stderr, "  Web search invoked (id=%s)\n", item.ID)
			default:
				// Log raw JSON for unknown types
				if rawJSON, err := json.Marshal(item); err == nil {
					fmt.Fprintf(os.Stderr, "  Raw: %s\n", string(rawJSON))
				}
			}
		}
		fmt.Fprintln(os.Stderr, "==============================")
	}

	// Find the function call output
	for _, item := range resp.Output {
		if item.Type == "function_call" && item.Name == "suggest_commands" {
			var result suggestionsResponse
			if err := json.Unmarshal([]byte(item.Arguments), &result); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}
			return result.Suggestions, nil
		}
	}

	return nil, fmt.Errorf("no suggest_commands function call in response")
}
