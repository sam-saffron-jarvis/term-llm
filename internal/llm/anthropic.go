package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/samsaffron/term-llm/internal/prompt"
)

// ListModels returns available models from Anthropic
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	page, err := p.client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}

	var models []ModelInfo
	for _, m := range page.Data {
		models = append(models, ModelInfo{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			Created:     m.CreatedAt.Unix(),
		})
	}

	return models, nil
}

// AnthropicProvider implements Provider using the Anthropic API
type AnthropicProvider struct {
	client         *anthropic.Client
	model          string
	thinkingBudget int64 // 0 = disabled, >0 = enabled with budget
}

// parseModelThinking extracts -thinking suffix from model name
// "claude-sonnet-4-5-thinking" -> ("claude-sonnet-4-5", 10000)
// "claude-sonnet-4-5" -> ("claude-sonnet-4-5", 0)
func parseModelThinking(model string) (string, int64) {
	if strings.HasSuffix(model, "-thinking") {
		return strings.TrimSuffix(model, "-thinking"), 10000
	}
	return model, 0
}

func NewAnthropicProvider(apiKey, model string) *AnthropicProvider {
	actualModel, thinkingBudget := parseModelThinking(model)
	client := anthropic.NewClient()
	return &AnthropicProvider{
		client:         &client,
		model:          actualModel,
		thinkingBudget: thinkingBudget,
	}
}

func (p *AnthropicProvider) Name() string {
	if p.thinkingBudget > 0 {
		return fmt.Sprintf("Anthropic (%s, thinking=%dk)", p.model, p.thinkingBudget/1000)
	}
	return fmt.Sprintf("Anthropic (%s)", p.model)
}

func (p *AnthropicProvider) SuggestCommands(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	if req.EnableSearch {
		return p.suggestWithSearch(ctx, req)
	}
	return p.suggestWithoutSearch(ctx, req)
}

func (p *AnthropicProvider) suggestWithoutSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	tool := anthropicSuggestTool(numSuggestions)

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, false)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Anthropic Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Tools: suggest_commands\n")
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "================================")
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 1024,
		System: []anthropic.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userPrompt)),
		},
		Tools: []anthropic.ToolUnionParam{tool},
	}

	// Add extended thinking if enabled
	// Note: Cannot force tool_choice when thinking is enabled, so we rely on prompt guidance
	if p.thinkingBudget > 0 {
		params.MaxTokens = 16000
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: p.thinkingBudget,
			},
		}
	} else {
		// Only force tool choice when thinking is disabled
		params.ToolChoice = anthropic.ToolChoiceParamOfTool(suggestCommandsToolName)
	}

	message, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Anthropic Response ===")
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				fmt.Fprintf(os.Stderr, "Tool: %s\n", block.Name)
				fmt.Fprintf(os.Stderr, "Arguments:\n%s\n", block.JSON.Input.Raw())
			} else if block.Type == "text" {
				fmt.Fprintf(os.Stderr, "Text: %s\n", block.Text)
			}
		}
		fmt.Fprintln(os.Stderr, "=================================")
	}

	return p.extractSuggestions(message.Content)
}

func (p *AnthropicProvider) suggestWithSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	suggestTool := anthropicSuggestToolBeta(numSuggestions)

	webSearchTool := anthropic.BetaToolUnionParam{
		OfWebSearchTool20250305: &anthropic.BetaWebSearchTool20250305Param{
			MaxUses: anthropic.Int(3),
		},
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, true)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Anthropic Request (with search) ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintln(os.Stderr, "Tools: web_search, suggest_commands")
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "===============================================")
	}

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		Betas:     []anthropic.AnthropicBeta{"web-search-2025-03-05"},
		System: []anthropic.BetaTextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userPrompt)),
		},
		Tools: []anthropic.BetaToolUnionParam{webSearchTool, suggestTool},
	}

	// Add extended thinking if enabled
	if p.thinkingBudget > 0 {
		params.MaxTokens = 16000
		params.Thinking = anthropic.BetaThinkingConfigParamUnion{
			OfEnabled: &anthropic.BetaThinkingConfigEnabledParam{
				BudgetTokens: p.thinkingBudget,
			},
		}
	}

	message, err := p.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Anthropic Response (with search) ===")
		fmt.Fprintf(os.Stderr, "Stop reason: %s\n", message.StopReason)
		for i, block := range message.Content {
			fmt.Fprintf(os.Stderr, "Block %d: type=%s\n", i, block.Type)
			switch block.Type {
			case "tool_use":
				fmt.Fprintf(os.Stderr, "  Tool: %s\n", block.Name)
				fmt.Fprintf(os.Stderr, "  Arguments:\n%s\n", block.JSON.Input.Raw())
			case "text":
				fmt.Fprintf(os.Stderr, "  Text: %s\n", block.Text)
			case "web_search_tool_result":
				fmt.Fprintf(os.Stderr, "  Search ID: %s\n", block.ToolUseID)
			case "server_tool_use":
				fmt.Fprintf(os.Stderr, "  Server Tool: %s (id=%s)\n", block.Name, block.ID)
			default:
				if rawJSON, err := json.Marshal(block); err == nil {
					fmt.Fprintf(os.Stderr, "  Raw: %s\n", string(rawJSON))
				}
			}
		}
		fmt.Fprintln(os.Stderr, "================================================")
	}

	return p.extractBetaSuggestions(message.Content)
}

func (p *AnthropicProvider) extractSuggestions(content []anthropic.ContentBlockUnion) ([]CommandSuggestion, error) {
	for _, block := range content {
		if block.Type == "tool_use" {
			var resp suggestionsResponse
			if err := json.Unmarshal([]byte(block.JSON.Input.Raw()), &resp); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}
			return resp.Suggestions, nil
		}
	}
	return nil, fmt.Errorf("no tool use in response")
}

func (p *AnthropicProvider) extractBetaSuggestions(content []anthropic.BetaContentBlockUnion) ([]CommandSuggestion, error) {
	for _, block := range content {
		if block.Type == "tool_use" && block.Name == suggestCommandsToolName {
			var resp suggestionsResponse
			if err := json.Unmarshal([]byte(block.JSON.Input.Raw()), &resp); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}
			return resp.Suggestions, nil
		}
	}
	return nil, fmt.Errorf("no suggest_commands tool use in response")
}

func (p *AnthropicProvider) StreamResponse(ctx context.Context, req AskRequest, output chan<- string) error {
	defer close(output)

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Anthropic Stream Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintf(os.Stderr, "Search: %v\n", req.EnableSearch)
		fmt.Fprintln(os.Stderr, "=======================================")
	}

	if req.EnableSearch {
		return p.streamWithSearch(ctx, req, output)
	}
	return p.streamWithoutSearch(ctx, req, output)
}

func (p *AnthropicProvider) streamWithoutSearch(ctx context.Context, req AskRequest, output chan<- string) error {
	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
		},
	}

	// Add system prompt if instructions provided
	if req.Instructions != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: req.Instructions},
		}
	}

	// Add extended thinking if enabled
	if p.thinkingBudget > 0 {
		params.MaxTokens = 16000
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: p.thinkingBudget,
			},
		}
	}

	stream := p.client.Messages.NewStreaming(ctx, params)

	for stream.Next() {
		event := stream.Current()
		// Skip thinking blocks - only output text
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			output <- event.Delta.Text
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("anthropic streaming error: %w", err)
	}

	return nil
}

func (p *AnthropicProvider) streamWithSearch(ctx context.Context, req AskRequest, output chan<- string) error {
	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	webSearchTool := anthropic.BetaToolUnionParam{
		OfWebSearchTool20250305: &anthropic.BetaWebSearchTool20250305Param{
			MaxUses: anthropic.Int(5),
		},
	}

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		Betas:     []anthropic.AnthropicBeta{"web-search-2025-03-05"},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userMessage)),
		},
		Tools: []anthropic.BetaToolUnionParam{webSearchTool},
	}

	// Add system prompt if instructions provided
	if req.Instructions != "" {
		params.System = []anthropic.BetaTextBlockParam{
			{Text: req.Instructions},
		}
	}

	// Add extended thinking if enabled
	if p.thinkingBudget > 0 {
		params.MaxTokens = 16000
		params.Thinking = anthropic.BetaThinkingConfigParamUnion{
			OfEnabled: &anthropic.BetaThinkingConfigEnabledParam{
				BudgetTokens: p.thinkingBudget,
			},
		}
	}

	stream := p.client.Beta.Messages.NewStreaming(ctx, params)

	for stream.Next() {
		event := stream.Current()
		// Skip thinking blocks - only output text
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			output <- event.Delta.Text
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("anthropic streaming error: %w", err)
	}

	return nil
}

// CallWithTool makes an API call with a single tool and returns raw results.
// Implements ToolCallProvider interface.
func (p *AnthropicProvider) CallWithTool(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	inputSchema := anthropic.ToolInputSchemaParam{
		Type:       "object",
		Properties: req.ToolSchema["properties"],
		Required:   req.ToolSchema["required"].([]string),
	}

	tool := anthropic.ToolUnionParamOfTool(inputSchema, req.ToolName)
	tool.OfTool.Description = anthropic.String(req.ToolDesc)

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: Anthropic %s Request ===\n", req.ToolName)
		fmt.Fprintf(os.Stderr, "System: %s\n", req.SystemPrompt)
		fmt.Fprintf(os.Stderr, "User: %s\n", req.UserPrompt)
		fmt.Fprintln(os.Stderr, "=====================================")
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: req.SystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.UserPrompt)),
		},
		Tools: []anthropic.ToolUnionParam{tool},
	}

	// Add extended thinking if enabled
	// Note: Cannot force tool_choice when thinking is enabled, so we rely on prompt guidance
	if p.thinkingBudget > 0 {
		params.MaxTokens = 16000
		params.Thinking = anthropic.ThinkingConfigParamUnion{
			OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: p.thinkingBudget,
			},
		}
	} else {
		// Only force tool choice when thinking is disabled
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
	}

	message, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintf(os.Stderr, "=== DEBUG: Anthropic %s Response ===\n", req.ToolName)
		fmt.Fprintf(os.Stderr, "Stop reason: %s\n", message.StopReason)
		for i, block := range message.Content {
			fmt.Fprintf(os.Stderr, "Block %d: type=%s\n", i, block.Type)
			if block.Type == "tool_use" {
				fmt.Fprintf(os.Stderr, "  Tool: %s\n", block.Name)
				fmt.Fprintf(os.Stderr, "  Input: %s\n", block.JSON.Input.Raw())
			}
		}
		fmt.Fprintln(os.Stderr, "======================================")
	}

	result := &ToolCallResult{}
	for _, block := range message.Content {
		if block.Type == "text" && block.Text != "" {
			result.TextOutput += block.Text + "\n"
		} else if block.Type == "tool_use" {
			result.ToolCalls = append(result.ToolCalls, ToolCallArguments{
				Name:      block.Name,
				Arguments: json.RawMessage(block.JSON.Input.Raw()),
			})
		}
	}

	return result, nil
}

// GetEdits calls the LLM with the edit tool and returns all proposed edits.
func (p *AnthropicProvider) GetEdits(ctx context.Context, systemPrompt, userPrompt string, debug bool) ([]EditToolCall, error) {
	return GetEditsFromProvider(ctx, p, systemPrompt, userPrompt, debug)
}

// GetUnifiedDiff calls the LLM with the unified_diff tool and returns the diff string.
func (p *AnthropicProvider) GetUnifiedDiff(ctx context.Context, systemPrompt, userPrompt string, debug bool) (string, error) {
	return GetUnifiedDiffFromProvider(ctx, p, systemPrompt, userPrompt, debug)
}
