package proxyprofile

import "github.com/term-llm/term-llm/internal/providerspec"

// Binding names a configuration API root and its corresponding credential.
// An empty Key means the integration uses a delegated account or no vendor key.
type Binding struct{ Root, Key string }

// Profile is public routing metadata, never a credential or a promise of live vendor certification.
type Profile struct {
	ID, Label, Upstream, Header, Prefix, Account, Billing string
	Provider                                              providerspec.Type
	WebSocket                                             bool
	Bindings                                              []Binding
	Gate                                                  string
	PathSuffix                                            string // Full-endpoint clients append this below their route.
	// CLIRootEnv names the provider Env entry that must carry this route's root to
	// a local CLI adapter. The adapter only leaves local-credential mode when it
	// is present, so configuration must set it whenever it binds the provider.
	CLIRootEnv string
}

// All returns a copy of the routing decisions. Each registered provider has a decision,
// including providers whose credentialless CLI transport still requires verification.
func All() []Profile {
	return []Profile{
		{ID: "anthropic", Label: "Anthropic API", Provider: providerspec.TypeAnthropic, Upstream: "https://api.anthropic.com", Header: "x-api-key", Billing: "pay_as_you_go"},
		{ID: "openai", Label: "OpenAI API", Provider: providerspec.TypeOpenAI, Upstream: "https://api.openai.com/v1", Header: "Authorization", Prefix: "Bearer ", WebSocket: true, Billing: "pay_as_you_go", Bindings: []Binding{{"image.openai.base_url", "image.openai.api_key"}, {"embed.openai.base_url", "embed.openai.api_key"}, {"live.openai.base_url", "live.openai.api_key"}}},
		{ID: "chatgpt", Label: "ChatGPT plan", Provider: providerspec.TypeChatGPT, Upstream: "https://chatgpt.com/backend-api/codex", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", WebSocket: true, Billing: "plan", Bindings: []Binding{{"image.chatgpt.base_url", ""}, {"live.chatgpt.call_base_url", ""}}},
		{ID: "chatgpt-backend", Label: "ChatGPT plan usage and dictation", Upstream: "https://chatgpt.com/backend-api", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", Billing: "plan", Bindings: []Binding{{"transcription.chatgpt.base_url", ""}}},
		{ID: "chatgpt-openai", Label: "ChatGPT OAuth embeddings and live (billing unspecified)", Upstream: "https://api.openai.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", Billing: "unspecified", WebSocket: true, Bindings: []Binding{{"embed.chatgpt.base_url", ""}, {"live.chatgpt.sideband_base_url", ""}}},
		{ID: "grok", Label: "Grok plan", Provider: providerspec.TypeGrok, Upstream: "https://cli-chat-proxy.grok.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "grok", Billing: "plan"},
		{ID: "copilot", Label: "GitHub Copilot plan", Provider: providerspec.TypeCopilot, Upstream: "https://api.githubcopilot.com", Header: "Authorization", Prefix: "Bearer ", Account: "copilot", Billing: "plan"},
		{ID: "gemini", Label: "Google Gemini", Provider: providerspec.TypeGemini, Upstream: "https://generativelanguage.googleapis.com/v1beta", Header: "x-goog-api-key", Billing: "pay_as_you_go", Bindings: []Binding{{"image.gemini.base_url", "image.gemini.api_key"}, {"audio.gemini.base_url", "audio.gemini.api_key"}, {"embed.gemini.base_url", "embed.gemini.api_key"}}},
		{ID: "gemini-live", Label: "Gemini Live", Upstream: "https://generativelanguage.googleapis.com", Header: "x-goog-api-key", Billing: "pay_as_you_go", WebSocket: true, PathSuffix: "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent", Bindings: []Binding{{"live.gemini.base_url", "live.gemini.api_key"}}},
		{ID: "openrouter", Label: "OpenRouter", Provider: providerspec.TypeOpenRouter, Upstream: "https://openrouter.ai/api/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"image.openrouter.base_url", "image.openrouter.api_key"}}},
		{ID: "xai", Label: "xAI API", Provider: providerspec.TypeXAI, Upstream: "https://api.x.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"image.xai.base_url", "image.xai.api_key"}}},
		{ID: "venice", Label: "Venice", Provider: providerspec.TypeVenice, Upstream: "https://api.venice.ai/api/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"image.venice.base_url", "image.venice.api_key"}, {"audio.venice.base_url", "audio.venice.api_key"}, {"music.venice.base_url", "music.venice.api_key"}, {"transcription.venice.base_url", "transcription.venice.api_key"}}},
		{ID: "nearai", Label: "NEAR AI", Provider: providerspec.TypeNearAI, Upstream: "https://cloud-api.near.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "sambanova", Label: "SambaNova", Provider: providerspec.TypeSambaNova, Upstream: "https://api.sambanova.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "obscurify", Label: "Obscurify", Provider: providerspec.TypeObscurify, Upstream: "https://obscurify.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "zen", Label: "OpenCode Zen", Provider: providerspec.TypeZen, Upstream: "https://opencode.ai/zen/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "opencode-go", Label: "OpenCode Go plan", Provider: providerspec.TypeOpenCodeGo, Upstream: "https://opencode.ai/zen/go/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "plan"},
		{ID: "opencode-go-messages", Label: "OpenCode Go plan · Messages", Upstream: "https://opencode.ai/zen/go", Header: "x-api-key", Billing: "plan"},
		{ID: "opencode-go-gemini", Label: "OpenCode Go plan · Gemini", Upstream: "https://opencode.ai/inference/go/v1", Header: "x-goog-api-key", Billing: "plan"},
		{ID: "bedrock", Label: "AWS Bedrock", Provider: providerspec.TypeBedrock, Header: "Authorization", Prefix: "Bearer ", Account: "aws-sigv4", Billing: "pay_as_you_go"},
		{ID: "ollama", Label: "Ollama at home", Provider: providerspec.TypeOllama, Upstream: "http://127.0.0.1:11434", Header: "Authorization", Prefix: "Bearer ", Billing: "local", Bindings: []Binding{{"embed.ollama.base_url", ""}}},
		{ID: "vllm", Label: "vLLM", Provider: providerspec.TypeVLLM, Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified"},
		{ID: "openai-compatible", Label: "OpenAI-compatible server", Provider: providerspec.TypeOpenAICompat, Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified"},
		{ID: "claude-code", Label: "Claude Code plan", Provider: providerspec.TypeClaudeBin, Upstream: "https://api.anthropic.com", Header: "Authorization", Prefix: "Bearer ", Account: "claude-code", Billing: "plan", CLIRootEnv: "TERM_LLM_CLAUDE_PROXY_ROOT"},
		// Cursor, Antigravity and Vibe stay gated until an isolated run of a supported real executable passes.
		{ID: "cursor-bin", Label: "Cursor plan", Provider: providerspec.TypeCursorBin, Upstream: "https://api2.cursor.sh", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "plan", CLIRootEnv: "TERM_LLM_CURSOR_PROXY_ROOT", Gate: "real cursor-agent runs stayed on the forced backend and HTTP/1 agent roots with every external connection refused, but the current 2026.11.13 release driven through the provider with its tool allowlist delivered the synthetic answer and never ended the turn, and 2026.02.27 predates the allowlist flag; releases dial a hardcoded api2.cursor.sh (diverted only by a best-effort proxy sink, so workers need OS egress denial), and the agent upstream origin, subscription token acceptance and refresh are unestablished"},
		{ID: "cursor-bin-agent", Label: "Cursor plan · agent", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "plan", Gate: "Cursor agent origin is server-advertised and has not been established"},
		{ID: "cursor-bin-repo", Label: "Cursor plan · repository", Upstream: "https://repo42.cursor.sh", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "plan", Gate: "requires the verified Cursor backend and agent routes"},
		// grok-bin is ungated on fixture evidence only: the real grok 1.0.77 executable completed model listing and an
		// ACP turn with a local MCP tool round trip against a synthetic gateway with all external connections refused.
		// Vendor acceptance of Grok plan credentials for this CLI's API-key wire shape has NOT been tested.
		{ID: "grok-bin", Label: "Grok CLI plan", Provider: providerspec.TypeGrokBin, Upstream: "https://cli-chat-proxy.grok.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "grok", Billing: "plan", CLIRootEnv: "TERM_LLM_GROK_CLI_PROXY_ROOT"},
		{ID: "agy-bin", Label: "Antigravity plan", Provider: providerspec.TypeAgyBin, Header: "Authorization", Prefix: "Bearer ", Billing: "plan", CLIRootEnv: "TERM_LLM_AGY_PROXY_ROOT", Gate: "no credentialless adapter exists: agy 1.3.3 (staged and version-checked in isolation) exposes no service-root or token override and signs in through the OS keyring with Google OAuth, so it needs trusted-proxy Google account custody behind TLS interception that has not been built or verified"},
		{ID: "vibe-bin", Label: "Mistral Vibe", Provider: providerspec.TypeVibeBin, Upstream: "https://api.mistral.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified", CLIRootEnv: "TERM_LLM_VIBE_PROXY_ROOT", Gate: "the real vibe-acp 3.1.2 reached only the scoped generic API-key route in a synthetic run and refused a built-in tool call, but it advertises its built-in file and shell tools to the model whatever enabled_tools or disabled_tools say, and bridged MCP tools are reachable only through its built-in run_typescript, which the strict bridge-only policy denies; subscription custody is vendor-dependent"},
		{ID: "elevenlabs", Label: "ElevenLabs", Upstream: "https://api.elevenlabs.io", Header: "xi-api-key", Billing: "pay_as_you_go", Bindings: []Binding{{"audio.elevenlabs.base_url", "audio.elevenlabs.api_key"}, {"music.elevenlabs.base_url", "music.elevenlabs.api_key"}, {"transcription.elevenlabs.base_url", "transcription.elevenlabs.api_key"}}},
		{ID: "flux", Label: "Flux", Upstream: "https://api.bfl.ai/v1", Header: "x-key", Billing: "pay_as_you_go", Bindings: []Binding{{"image.flux.base_url", "image.flux.api_key"}}},
		{ID: "jina", Label: "Jina embeddings", Upstream: "https://api.jina.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"embed.jina.base_url", "embed.jina.api_key"}}},
		{ID: "voyage", Label: "Voyage embeddings", Upstream: "https://api.voyageai.com/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"embed.voyage.base_url", "embed.voyage.api_key"}}},
		{ID: "mistral", Label: "Mistral transcription", Upstream: "https://api.mistral.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go", Bindings: []Binding{{"providers.mistral.base_url", "providers.mistral.api_key"}}},
	}
}

// ForProvider reports the explicit proxy decision for a provider type.
func ForProvider(kind providerspec.Type) (Profile, bool) {
	for _, p := range All() {
		if p.Provider == kind && kind != "" {
			return p, true
		}
	}
	return Profile{}, false
}
