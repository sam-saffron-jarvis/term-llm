package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/samsaffron/term-llm/internal/prompt"
	"google.golang.org/genai"
)

// GeminiProvider implements Provider using the Google Gemini API
type GeminiProvider struct {
	apiKey string
	model  string
}

func NewGeminiProvider(apiKey, model string, _ bool) *GeminiProvider {
	return &GeminiProvider{
		apiKey: apiKey,
		model:  model,
	}
}

func (p *GeminiProvider) Name() string {
	return fmt.Sprintf("Gemini (%s)", p.model)
}

func (p *GeminiProvider) newClient(ctx context.Context) (*genai.Client, error) {
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  p.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
}

func (p *GeminiProvider) SuggestCommands(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	if req.EnableSearch {
		return p.suggestWithSearch(ctx, req)
	}
	return p.suggestWithoutSearch(ctx, req)
}

// performSearch performs a Google Search query and returns the search context
func (p *GeminiProvider) performSearch(ctx context.Context, query string, debug bool) (string, error) {
	client, err := p.newClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create gemini client: %w", err)
	}

	searchConfig := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{}},
		},
	}

	searchPrompt := fmt.Sprintf("Search for current information about: %s\n\nProvide a concise summary of the most relevant and up-to-date information found.", query)

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Search Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Query: %s\n", query)
		fmt.Fprintln(os.Stderr, "=====================================")
	}

	resp, err := client.Models.GenerateContent(ctx, p.model, genai.Text(searchPrompt), searchConfig)
	if err != nil {
		return "", fmt.Errorf("search API error: %w", err)
	}

	searchResult := resp.Text()

	if debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Search Response ===")
		fmt.Fprintf(os.Stderr, "Result: %s\n", searchResult)
		fmt.Fprintln(os.Stderr, "======================================")
	}

	return searchResult, nil
}

func (p *GeminiProvider) suggestWithSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	// Phase 1: Perform search to get current information
	searchContext, err := p.performSearch(ctx, req.UserInput, req.Debug)
	if err != nil {
		// If search fails, fall back to suggestions without search
		if req.Debug {
			fmt.Fprintf(os.Stderr, "Search failed, falling back: %v\n", err)
		}
		return p.suggestWithoutSearch(ctx, req)
	}

	// Phase 2: Generate suggestions with search context
	client, err := p.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	// Define the function schema for structured output
	suggestTool := &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "suggest_commands",
				Description: "Suggest shell commands based on user input",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"suggestions": {
							Type:        genai.TypeArray,
							Description: "List of command suggestions",
							Items: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"command": {
										Type:        genai.TypeString,
										Description: "The shell command to execute",
									},
									"explanation": {
										Type:        genai.TypeString,
										Description: "Brief explanation of what the command does",
									},
									"likelihood": {
										Type:        genai.TypeInteger,
										Description: "How likely this command matches user intent (1=unlikely, 10=very likely)",
									},
								},
								Required: []string{"command", "explanation", "likelihood"},
							},
						},
					},
					Required: []string{"suggestions"},
				},
			},
		},
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, true)

	// Include search results in the user prompt
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)
	if searchContext != "" {
		userPrompt = fmt.Sprintf("%s\n\n<search_results>\n%s\n</search_results>", userPrompt, searchContext)
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
		Tools:             []*genai.Tool{suggestTool},
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{"suggest_commands"},
			},
		},
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Request (with search) ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Tools: suggest_commands\n")
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "============================================")
	}

	resp, err := client.Models.GenerateContent(ctx, p.model, genai.Text(userPrompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Response (with search) ===")
		p.debugPrintResponse(resp)
		fmt.Fprintln(os.Stderr, "=============================================")
	}

	return p.extractSuggestions(resp)
}

func (p *GeminiProvider) suggestWithoutSearch(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	client, err := p.newClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}

	numSuggestions := req.NumSuggestions
	if numSuggestions <= 0 {
		numSuggestions = 3
	}

	// Define the function schema for structured output
	suggestTool := &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{
				Name:        "suggest_commands",
				Description: "Suggest shell commands based on user input",
				Parameters: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"suggestions": {
							Type:        genai.TypeArray,
							Description: "List of command suggestions",
							Items: &genai.Schema{
								Type: genai.TypeObject,
								Properties: map[string]*genai.Schema{
									"command": {
										Type:        genai.TypeString,
										Description: "The shell command to execute",
									},
									"explanation": {
										Type:        genai.TypeString,
										Description: "Brief explanation of what the command does",
									},
									"likelihood": {
										Type:        genai.TypeInteger,
										Description: "How likely this command matches user intent (1=unlikely, 10=very likely)",
									},
								},
								Required: []string{"command", "explanation", "likelihood"},
							},
						},
					},
					Required: []string{"suggestions"},
				},
			},
		},
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, false)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput, req.Files, req.Stdin)

	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, genai.RoleUser),
		Tools:             []*genai.Tool{suggestTool},
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode:                 genai.FunctionCallingConfigModeAny,
				AllowedFunctionNames: []string{"suggest_commands"},
			},
		},
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Tools: suggest_commands\n")
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "==============================")
	}

	resp, err := client.Models.GenerateContent(ctx, p.model, genai.Text(userPrompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Response ===")
		p.debugPrintResponse(resp)
		fmt.Fprintln(os.Stderr, "===============================")
	}

	return p.extractSuggestions(resp)
}

func (p *GeminiProvider) debugPrintResponse(resp *genai.GenerateContentResponse) {
	for _, fc := range resp.FunctionCalls() {
		fmt.Fprintf(os.Stderr, "Function: %s\n", fc.Name)
		argsJSON, _ := json.Marshal(fc.Args)
		fmt.Fprintf(os.Stderr, "Arguments:\n%s\n", string(argsJSON))
	}
	if text := resp.Text(); text != "" {
		fmt.Fprintf(os.Stderr, "Text: %s\n", text)
	}
}

func (p *GeminiProvider) extractSuggestions(resp *genai.GenerateContentResponse) ([]CommandSuggestion, error) {
	for _, fc := range resp.FunctionCalls() {
		if fc.Name == "suggest_commands" {
			// Extract suggestions from the function call arguments
			suggestionsRaw, ok := fc.Args["suggestions"]
			if !ok {
				return nil, fmt.Errorf("no suggestions in function call response")
			}

			// Convert to JSON and back to parse into our struct
			jsonBytes, err := json.Marshal(suggestionsRaw)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal suggestions: %w", err)
			}

			var suggestions []CommandSuggestion
			if err := json.Unmarshal(jsonBytes, &suggestions); err != nil {
				return nil, fmt.Errorf("failed to parse suggestions: %w", err)
			}

			return suggestions, nil
		}
	}
	return nil, fmt.Errorf("no function call in response")
}

func (p *GeminiProvider) StreamResponse(ctx context.Context, req AskRequest, output chan<- string) error {
	defer close(output)

	if req.EnableSearch {
		return p.streamWithSearch(ctx, req, output)
	}
	return p.streamWithoutSearch(ctx, req, output)
}

func (p *GeminiProvider) streamWithoutSearch(ctx context.Context, req AskRequest, output chan<- string) error {
	client, err := p.newClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %w", err)
	}

	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	config := &genai.GenerateContentConfig{}

	// Add system prompt if instructions provided
	if req.Instructions != "" {
		config.SystemInstruction = genai.NewContentFromText(req.Instructions, genai.RoleUser)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Stream Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintln(os.Stderr, "=====================================")
	}

	for resp, err := range client.Models.GenerateContentStream(ctx, p.model, genai.Text(userMessage), config) {
		if err != nil {
			return fmt.Errorf("gemini streaming error: %w", err)
		}
		if text := resp.Text(); text != "" {
			output <- text
		}
	}

	return nil
}

func (p *GeminiProvider) streamWithSearch(ctx context.Context, req AskRequest, output chan<- string) error {
	client, err := p.newClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %w", err)
	}

	userMessage := prompt.AskUserPrompt(req.Question, req.Files, req.Stdin)

	config := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{}},
		},
	}

	// Add system prompt if instructions provided
	if req.Instructions != "" {
		config.SystemInstruction = genai.NewContentFromText(req.Instructions, genai.RoleUser)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Stream Request (with search) ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintln(os.Stderr, "===================================================")
	}

	var sources []string
	for resp, err := range client.Models.GenerateContentStream(ctx, p.model, genai.Text(userMessage), config) {
		if err != nil {
			return fmt.Errorf("gemini streaming error: %w", err)
		}
		if text := resp.Text(); text != "" {
			output <- text
		}

		// Collect grounding sources
		for _, cand := range resp.Candidates {
			if cand.GroundingMetadata != nil && cand.GroundingMetadata.GroundingChunks != nil {
				for _, chunk := range cand.GroundingMetadata.GroundingChunks {
					if chunk.Web != nil && chunk.Web.URI != "" {
						title := chunk.Web.Title
						if title == "" {
							title = "Source"
						}
						source := fmt.Sprintf("[%s](%s)", title, chunk.Web.URI)
						// Avoid duplicates
						found := false
						for _, s := range sources {
							if s == source {
								found = true
								break
							}
						}
						if !found {
							sources = append(sources, source)
						}
					}
				}
			}
		}
	}

	// Output sources at the end
	if len(sources) > 0 {
		output <- "\n\n**Sources:**\n"
		for _, source := range sources {
			output <- "- " + source + "\n"
		}
	}

	return nil
}
