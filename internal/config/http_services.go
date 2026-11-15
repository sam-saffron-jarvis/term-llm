package config

// ImageConfig configures image generation settings
type ImageConfig struct {
	Provider   string                `mapstructure:"provider"`   // built-in image provider or named providers entry with type: openai_compatible
	OutputDir  string                `mapstructure:"output_dir"` // default save directory
	Gemini     ImageGeminiConfig     `mapstructure:"gemini"`
	OpenAI     ImageOpenAIConfig     `mapstructure:"openai"`
	ChatGPT    ImageChatGPTConfig    `mapstructure:"chatgpt"`
	XAI        ImageXAIConfig        `mapstructure:"xai"`
	Venice     ImageVeniceConfig     `mapstructure:"venice"`
	Flux       ImageFluxConfig       `mapstructure:"flux"`
	OpenRouter ImageOpenRouterConfig `mapstructure:"openrouter"`
	Debug      ImageDebugConfig      `mapstructure:"debug"`
}

// ImageGeminiConfig configures Gemini image generation
type ImageGeminiConfig struct {
	BaseURL   string `mapstructure:"base_url"`
	APIKey    string `mapstructure:"api_key"`
	Model     string `mapstructure:"model"`
	ImageSize string `mapstructure:"image_size"` // Default image size: 1K, 2K, 4K
}

// ImageOpenAIConfig configures OpenAI image generation
type ImageOpenAIConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// ImageChatGPTConfig configures subscription-backed image generation through
// the ChatGPT Codex Images API (OAuth, no API key required).
type ImageChatGPTConfig struct {
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"` // e.g., gpt-image-2.5-flare (default) or gpt-image-2.5-sunburst
}

// ImageXAIConfig configures xAI (Grok) image generation
type ImageXAIConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // grok-2-image or grok-2-image-1212
}

// ImageVeniceConfig configures Venice AI image generation
type ImageVeniceConfig struct {
	BaseURL    string `mapstructure:"base_url"`
	APIKey     string `mapstructure:"api_key"`
	Model      string `mapstructure:"model"`
	EditModel  string `mapstructure:"edit_model"`
	Resolution string `mapstructure:"resolution"`
}

// ImageFluxConfig configures Flux (Black Forest Labs) image generation
type ImageFluxConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // flux-2-pro for generation, flux-kontext-pro for editing
}

// ImageOpenRouterConfig configures OpenRouter image generation
type ImageOpenRouterConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // e.g., google/gemini-2.5-flash-image
}

// ImageDebugConfig configures the debug image provider (local random images)
type ImageDebugConfig struct {
	Delay float64 `mapstructure:"delay"` // delay in seconds before returning (e.g., 1.5)
}

// AudioConfig configures speech/audio generation settings.
type AudioConfig struct {
	Provider   string                `mapstructure:"provider"`   // default audio provider: venice, gemini, or elevenlabs
	OutputDir  string                `mapstructure:"output_dir"` // default save directory
	Venice     AudioVeniceConfig     `mapstructure:"venice"`
	Gemini     AudioGeminiConfig     `mapstructure:"gemini"`
	ElevenLabs AudioElevenLabsConfig `mapstructure:"elevenlabs"`
}

// AudioVeniceConfig configures Venice AI text-to-speech generation.
type AudioVeniceConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Voice   string `mapstructure:"voice"`
	Format  string `mapstructure:"format"`
}

// AudioGeminiConfig configures Gemini text-to-speech generation.
type AudioGeminiConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Voice   string `mapstructure:"voice"`
	Format  string `mapstructure:"format"`
}

// AudioElevenLabsConfig configures ElevenLabs text-to-speech generation.
type AudioElevenLabsConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Voice   string `mapstructure:"voice"`
	Format  string `mapstructure:"format"`
}

// MusicConfig configures music and sound-effect generation settings.
type MusicConfig struct {
	Provider   string                `mapstructure:"provider"`   // default music provider: venice or elevenlabs
	OutputDir  string                `mapstructure:"output_dir"` // default save directory
	Venice     MusicVeniceConfig     `mapstructure:"venice"`
	ElevenLabs MusicElevenLabsConfig `mapstructure:"elevenlabs"`
}

// MusicVeniceConfig configures Venice music/audio generation.
type MusicVeniceConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Format  string `mapstructure:"format"`
}

// MusicElevenLabsConfig configures ElevenLabs music generation.
type MusicElevenLabsConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Format  string `mapstructure:"format"`
}

// TranscriptionConfig configures audio transcription settings.
type TranscriptionConfig struct {
	Provider   string                        `mapstructure:"provider"`   // named provider from providers map; default "openai"
	Model      string                        `mapstructure:"model"`      // optional model override
	SaveDir    string                        `mapstructure:"save_dir"`   // if set, persist each uploaded audio file here
	Timestamps bool                          `mapstructure:"timestamps"` // Request timestamp metadata where supported
	ChatGPT    TranscriptionChatGPTConfig    `mapstructure:"chatgpt"`
	Venice     TranscriptionVeniceConfig     `mapstructure:"venice"`
	ElevenLabs TranscriptionElevenLabsConfig `mapstructure:"elevenlabs"`
}

// TranscriptionVeniceConfig configures Venice speech-to-text.
type TranscriptionVeniceConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// TranscriptionElevenLabsConfig configures ElevenLabs speech-to-text.
type TranscriptionElevenLabsConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// EmbedConfig configures text embedding generation
type EmbedConfig struct {
	Provider  string             `mapstructure:"provider"`   // default embedding provider: gemini, openai, chatgpt, venice, jina, voyage, ollama
	BatchSize int                `mapstructure:"batch_size"` // document embedding batch size; default 32
	OpenAI    EmbedOpenAIConfig  `mapstructure:"openai"`
	ChatGPT   EmbedChatGPTConfig `mapstructure:"chatgpt"`
	Gemini    EmbedGeminiConfig  `mapstructure:"gemini"`
	Jina      EmbedJinaConfig    `mapstructure:"jina"`
	Voyage    EmbedVoyageConfig  `mapstructure:"voyage"`
	Ollama    EmbedOllamaConfig  `mapstructure:"ollama"`
}

// EmbedOpenAIConfig configures OpenAI embedding generation
type EmbedOpenAIConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // text-embedding-3-small (default), text-embedding-3-large
}

// EmbedGeminiConfig configures Gemini embedding generation
type EmbedGeminiConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // gemini-embedding-001 (default)
}

// EmbedJinaConfig configures Jina AI embedding generation
type EmbedJinaConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // jina-embeddings-v3 (default), jina-embeddings-v4
}

// EmbedVoyageConfig configures Voyage AI embedding generation
type EmbedVoyageConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"` // voyage-3.5 (default), voyage-3-large, voyage-code-3
}

// EmbedOllamaConfig configures Ollama embedding generation
type EmbedOllamaConfig struct {
	BaseURL string `mapstructure:"base_url"` // default: http://127.0.0.1:11434
	Model   string `mapstructure:"model"`    // nomic-embed-text (default)
}

// SearchConfig configures web search providers
type SearchConfig struct {
	Provider      string                 `mapstructure:"provider"`       // exa_mcp (default), exa, perplexity, parallel, tavily, brave, google, youcom, duckduckgo
	FetchProvider string                 `mapstructure:"fetch_provider"` // jina (default), exa_mcp, none
	ForceExternal bool                   `mapstructure:"force_external"` // force external search for all providers
	Exa           SearchExaConfig        `mapstructure:"exa"`
	ExaMCP        SearchExaMCPConfig     `mapstructure:"exa_mcp"`
	Perplexity    SearchPerplexityConfig `mapstructure:"perplexity"`
	Parallel      SearchParallelConfig   `mapstructure:"parallel"`
	Tavily        SearchTavilyConfig     `mapstructure:"tavily"`
	Brave         SearchBraveConfig      `mapstructure:"brave"`
	Google        SearchGoogleConfig     `mapstructure:"google"`
	YouCom        SearchYouComConfig     `mapstructure:"youcom"`
	DuckDuckGo    SearchDuckDuckGoConfig `mapstructure:"duckduckgo"`
	Jina          SearchJinaConfig       `mapstructure:"jina"`
}

// SearchExaConfig configures Exa search
type SearchExaConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchExaMCPConfig configures Exa MCP search/fetch
type SearchExaMCPConfig struct {
	URL    string `mapstructure:"url"`
	APIKey string `mapstructure:"api_key"`
}

// SearchPerplexityConfig configures Perplexity search
type SearchPerplexityConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchParallelConfig configures Parallel search
type SearchParallelConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchTavilyConfig configures Tavily search
type SearchTavilyConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchBraveConfig configures Brave search
type SearchBraveConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchYouComConfig configures You.com search. The API key is optional:
// without one, the keyless free tier is used.
type SearchYouComConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// SearchGoogleConfig configures Google Custom Search
type SearchGoogleConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	CX      string `mapstructure:"cx"` // Custom Search Engine ID
}

// SearchDuckDuckGoConfig configures the DuckDuckGo Lite API root.
type SearchDuckDuckGoConfig struct {
	BaseURL string `mapstructure:"base_url"`
}

// SearchJinaConfig configures the Jina Reader API root.
type SearchJinaConfig struct {
	BaseURL string `mapstructure:"base_url"`
}

// EmbedChatGPTConfig configures the OAuth embedding API root independently of Platform API keys.
type EmbedChatGPTConfig struct {
	BaseURL string `mapstructure:"base_url"`
}

// TranscriptionChatGPTConfig selects the dictation backend independently of Codex inference.
type TranscriptionChatGPTConfig struct {
	BaseURL string `mapstructure:"base_url"`
}
