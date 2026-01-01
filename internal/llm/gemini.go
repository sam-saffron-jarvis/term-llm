package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/generative-ai-go/genai"
	"github.com/samsaffron/term-llm/internal/prompt"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
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

func (p *GeminiProvider) SuggestCommands(ctx context.Context, req SuggestRequest) ([]CommandSuggestion, error) {
	client, err := genai.NewClient(ctx, option.WithAPIKey(p.apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(p.model)

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

	model.Tools = []*genai.Tool{suggestTool}
	model.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode:                 genai.FunctionCallingAny,
			AllowedFunctionNames: []string{"suggest_commands"},
		},
	}

	systemPrompt := prompt.SuggestSystemPrompt(req.Shell, req.Instructions, numSuggestions, false)
	userPrompt := prompt.SuggestUserPrompt(req.UserInput)

	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(systemPrompt)},
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Tools: suggest_commands\n")
		fmt.Fprintf(os.Stderr, "System:\n%s\n", systemPrompt)
		fmt.Fprintf(os.Stderr, "User:\n%s\n", userPrompt)
		fmt.Fprintln(os.Stderr, "==============================")
	}

	resp, err := model.GenerateContent(ctx, genai.Text(userPrompt))
	if err != nil {
		return nil, fmt.Errorf("gemini API error: %w", err)
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Response ===")
		for _, cand := range resp.Candidates {
			for _, part := range cand.Content.Parts {
				switch v := part.(type) {
				case genai.FunctionCall:
					fmt.Fprintf(os.Stderr, "Function: %s\n", v.Name)
					argsJSON, _ := json.Marshal(v.Args)
					fmt.Fprintf(os.Stderr, "Arguments:\n%s\n", string(argsJSON))
				case genai.Text:
					fmt.Fprintf(os.Stderr, "Text: %s\n", v)
				}
			}
		}
		fmt.Fprintln(os.Stderr, "===============================")
	}

	return p.extractSuggestions(resp)
}

func (p *GeminiProvider) extractSuggestions(resp *genai.GenerateContentResponse) ([]CommandSuggestion, error) {
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if fc, ok := part.(genai.FunctionCall); ok && fc.Name == "suggest_commands" {
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
	}
	return nil, fmt.Errorf("no function call in response")
}

func (p *GeminiProvider) StreamResponse(ctx context.Context, req AskRequest, output chan<- string) error {
	defer close(output)

	client, err := genai.NewClient(ctx, option.WithAPIKey(p.apiKey))
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %w", err)
	}
	defer client.Close()

	model := client.GenerativeModel(p.model)

	// Add system prompt if instructions provided
	if req.Instructions != "" {
		model.SystemInstruction = &genai.Content{
			Parts: []genai.Part{genai.Text(req.Instructions)},
		}
	}

	if req.Debug {
		fmt.Fprintln(os.Stderr, "=== DEBUG: Gemini Stream Request ===")
		fmt.Fprintf(os.Stderr, "Provider: %s\n", p.Name())
		fmt.Fprintf(os.Stderr, "Question: %s\n", req.Question)
		fmt.Fprintln(os.Stderr, "=====================================")
	}

	iter := model.GenerateContentStream(ctx, genai.Text(req.Question))

	for {
		resp, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("gemini streaming error: %w", err)
		}

		for _, cand := range resp.Candidates {
			for _, part := range cand.Content.Parts {
				if text, ok := part.(genai.Text); ok {
					output <- string(text)
				}
			}
		}
	}

	return nil
}
