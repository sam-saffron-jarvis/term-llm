package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/samsaffron/term-llm/internal/prompt"
)

// OpenAIProvider implements Provider using the standard OpenAI API
type OpenAIProvider struct {
	client *openai.Client
	model  string
	effort string // reasoning effort: "low", "medium", "high", "xhigh", or ""
}

// parseModelEffort extracts effort suffix from model name
// "gpt-5.2-high" -> ("gpt-5.2", "high")
// "gpt-5.2-xhigh" -> ("gpt-5.2", "xhigh")
// "gpt-5.2" -> ("gpt-5.2", "")
func parseModelEffort(model string) (string, string) {
	// Check suffixes in order from longest to shortest to avoid "-high" matching "-xhigh"
	suffixes := []string{"xhigh", "medium", "high", "low"}
	for _, effort := range suffixes {
		suffix := "-" + effort
		if strings.HasSuffix(model, suffix) {
			return strings.TrimSuffix(model, suffix), effort
		}
	}
	return model, ""
}

func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	actualModel, effort := parseModelEffort(model)
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAIProvider{
		client: &client,
		model:  actualModel,
		effort: effort,
	}
}

func (p *OpenAIProvider) Name() string {
	if p.effort != "" {
		return fmt.Sprintf("OpenAI (%s, effort=%s)", p.model, p.effort)
	}
	return fmt.Sprintf("OpenAI (%s)", p.model)
}

func (p *OpenAIProvider) SuggestCommands(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	// Define the function tool for structured output
	functionTool := responses.ToolParamOfFunction(
		"suggest_commands",
		prompt.SuggestSchema(numSuggestions),
		true,
	)
	functionTool.OfFunction.Description = openai.String("Suggest shell commands based on user input")

	tools := []responses.ToolUnionParam{functionTool}

	// Add web search tool if enabled
	if req.EnableSearch {
		webSearchTool := responses.ToolParamOfWebSearchPreview(responses.WebSearchToolTypeWebSearchPreview)
		tools = []responses.ToolUnionParam{webSearchTool, functionTool}
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, req.EnableSearch)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: OpenAI Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		if req.EnableSearch {
			fmt.Fprintln(os.Stderr, "Tools: web_search_preview, suggest_commands")
		} else {
			fmt.Fprintln(os.Stderr, "Tools: suggest_commands")
		}
		fmt.Fprintf(os.Stderr, "Instructions:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "Input:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "=============================")
	}

	params := responses.ResponseNewParams{
		Model:        shared.ResponsesModel(p.model),
		Instructions: openai.String(systemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(userPrompt),
		},
		Tools: tools,
	}

	// Add reasoning effort if set
	if p.effort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(p.effort),
		}
	}

	resp, err := p.client.Responses.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}

	if req.Debug {
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

// callWithTool makes an API call with a single tool and returns raw results
func (p *OpenAIProvider) callWithTool(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	tool := responses.ToolParamOfFunction(req.ToolName, req.ToolSchema, true)
	tool.OfFunction.Description = openai.String(req.ToolDesc)

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: OpenAI %s Request ===\n", req.ToolName)
		fmt.Fprintf(os.Stderr, "System: %s\n", req.SystemPrompt)
		fmt.Fprintf(os.Stderr, "User: %s\n", req.UserPrompt)
		fmt.Fprintln(os.Stderr, "==================================")
	}

	params := responses.ResponseNewParams{
		Model:        shared.ResponsesModel(p.model),
		Instructions: openai.String(req.SystemPrompt),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(req.UserPrompt),
		},
		Tools: []responses.ToolUnionParam{tool},
	}

	// Add reasoning effort if set
	if p.effort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(p.effort),
		}
	}

	resp, err := p.client.Responses.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: OpenAI %s Response ===\n", req.ToolName)
		fmt.Fprintf(os.Stderr, "Status: %s\n", resp.Status)
		for i, item := range resp.Output {
			fmt.Fprintf(os.Stderr, "Output %d: type=%s\n", i, item.Type)
			if item.Type == "function_call" {
				fmt.Fprintf(os.Stderr, "  Function: %s\n", item.Name)
				fmt.Fprintf(os.Stderr, "  Arguments: %s\n", item.Arguments)
			}
		}
		fmt.Fprintln(os.Stderr, "===================================")
	}

	result := &ToolCallResult{}
	for _, item := range resp.Output {
		if item.Type == "message" {
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					result.TextOutput += content.Text + "\n"
				}
			}
		} else if item.Type == "function_call" {
			result.ToolCalls = append(result.ToolCalls, ToolCallArguments{
				Name:      item.Name,
				Arguments: json.RawMessage(item.Arguments),
			})
		}
	}

	return result, nil
}

// GetEdits calls the LLM with the edit tool and returns all proposed edits
func (p *OpenAIProvider) GetEdits(ctx context.Context, systemPrompt, userPrompt string, debug bool) ([]EditToolCall, error) {
	result, err := p.callWithTool(ctx, ToolCallRequest{
		SystemPrompt: systemPrompt, UserPrompt: userPrompt,
		ToolName: "edit", ToolDesc: prompt.EditDescription,
		ToolSchema: prompt.EditSchema(), Debug: debug,
	})
	if err != nil {
		return nil, err
	}
	if result.TextOutput != "" {
		fmt.Print(result.TextOutput)
	}
	return ParseEditToolCalls(result.ToolCalls), nil
}

// GetUnifiedDiff calls the LLM with the unified_diff tool and returns the diff string.
func (p *OpenAIProvider) GetUnifiedDiff(ctx context.Context, systemPrompt, userPrompt string, debug bool) (string, error) {
	result, err := p.callWithTool(ctx, ToolCallRequest{
		SystemPrompt: systemPrompt, UserPrompt: userPrompt,
		ToolName: "unified_diff", ToolDesc: prompt.UnifiedDiffDescription,
		ToolSchema: prompt.UnifiedDiffSchema(), Debug: debug,
	})
	if err != nil {
		return "", err
	}
	if result.TextOutput != "" {
		fmt.Print(result.TextOutput)
	}
	return ParseUnifiedDiff(result.ToolCalls)
}

func (p *OpenAIProvider) StreamResponse(ctx context.Context, req AskRequest, output chan<- string) error {
	defer close(output)

	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: OpenAI Stream Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintf(os.Stderr, "Search: %v\n", req.EnableSearch)
		fmt.Fprintln(os.Stderr, "====================================")
	}

	params := responses.ResponseNewParams{
		Model: shared.ResponsesModel(p.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(userMessage),
		},
	}

	if req.EnableSearch {
		webSearchTool := responses.ToolParamOfWebSearchPreview(responses.WebSearchToolTypeWebSearchPreview)
		params.Tools = []responses.ToolUnionParam{webSearchTool}
	}

	// Add reasoning effort if set
	if p.effort != "" {
		params.Reasoning = shared.ReasoningParam{
			Effort: shared.ReasoningEffort(p.effort),
		}
	}

	stream := p.client.Responses.NewStreaming(ctx, params)

	for stream.Next() {
		event := stream.Current()
		if event.Type == "response.output_text.delta" && event.Text != "" {
			output <- event.Text
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("openai streaming error: %w", err)
	}

	return nil
}
