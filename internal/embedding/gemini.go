package embedding

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/genai"
)

const (
	geminiDefaultModel = "gemini-embedding-001"
	geminiEmbedTimeout = 2 * time.Minute
)

// GeminiProvider implements EmbeddingProvider using Google's Gemini API
type GeminiProvider struct {
	apiKey string
	model  string
}

func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey: apiKey,
		model:  geminiDefaultModel,
	}
}

func (p *GeminiProvider) Name() string {
	return "Gemini"
}

func (p *GeminiProvider) DefaultModel() string {
	return geminiDefaultModel
}

func (p *GeminiProvider) Embed(req EmbedRequest) (*EmbeddingResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), geminiEmbedTimeout)
	defer cancel()

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  p.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("Gemini client error: %w", err)
	}

	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	// Build content objects for each text
	contents := make([]*genai.Content, len(req.Texts))
	for i, text := range req.Texts {
		contents[i] = genai.NewContentFromText(text, genai.RoleUser)
	}

	// Build config
	var embedConfig *genai.EmbedContentConfig
	if req.TaskType != "" || req.Dimensions > 0 {
		embedConfig = &genai.EmbedContentConfig{}
		if req.TaskType != "" {
			embedConfig.TaskType = req.TaskType
		}
		if req.Dimensions > 0 {
			dim := int32(req.Dimensions)
			embedConfig.OutputDimensionality = &dim
		}
	}

	resp, err := client.Models.EmbedContent(ctx, model, contents, embedConfig)
	if err != nil {
		return nil, fmt.Errorf("Gemini embedding API error: %w", err)
	}

	result := &EmbeddingResult{
		Model:      model,
		Embeddings: make([]Embedding, len(resp.Embeddings)),
	}

	for i, emb := range resp.Embeddings {
		// Convert float32 to float64
		vec := make([]float64, len(emb.Values))
		for j, v := range emb.Values {
			vec[j] = float64(v)
		}

		result.Embeddings[i] = Embedding{
			Index:  i,
			Vector: vec,
		}
		if i < len(req.Texts) {
			result.Embeddings[i].Text = req.Texts[i]
		}
	}

	if len(result.Embeddings) > 0 {
		result.Dimensions = len(result.Embeddings[0].Vector)
	}

	return result, nil
}
