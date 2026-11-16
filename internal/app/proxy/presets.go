package proxy

import "github.com/term-llm/term-llm/internal/proxyprofile"

// routePreset only supplies editable creation-time defaults. Forwarding and
// validation remain provider-independent; no credentials or client state live here.
//
// Auth is the local client authentication format. It is also the upstream
// credential substitution, unless AccountKind names a trusted account (the
// account integration writes those headers) or Auth is empty (no upstream key).
type routePreset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Upstream    string `json:"upstream"`
	Auth        Auth   `json:"auth"`
	Note        string `json:"note,omitempty"`
	Billing     string `json:"billing,omitempty"`
	Usage       string `json:"usage,omitempty"`
	WebSocket   bool   `json:"websocket,omitempty"`
	AccountKind string `json:"account_kind,omitempty"`
}

// routePresets is a projection of proxyprofile.All, the routing table that
// native term-llm clients bind to, so the two cannot drift.
var routePresets = presetsFromProfiles(proxyprofile.All())

// presetNotes holds operator guidance that the routing table does not carry.
var presetNotes = map[string]string{
	"openai":               "Set your client's base URL to this route's local prefix, without adding /v1 again.",
	"anthropic":            "The client sends anthropic-version and beta headers unchanged. Put the client token in the client's API key field.",
	"gemini":               "Use a header-based API key, not ?key=. The local route replaces the /v1beta API root.",
	"gemini-live":          "Use a header-based API key, not ?key=.",
	"xai":                  "API key access, not a Grok plan login.",
	"chatgpt":              "Only the Codex responses, models, image and realtime-call endpoints are forwarded.",
	"chatgpt-backend":      "Only plan usage and transcription endpoints are forwarded.",
	"chatgpt-openai":       "Only embeddings and realtime endpoints are forwarded. How OpenAI bills these for a ChatGPT login is not established, so billing stays unspecified.",
	"grok":                 "Only the responses, chat completions, models and billing endpoints are forwarded.",
	"copilot":              "Only the responses, chat completions and models endpoints are forwarded.",
	"claude-code":          "Only the Messages, token counting, models and plan usage/profile endpoints are forwarded.",
	"bedrock":              "Enter https://bedrock-runtime.<region>.amazonaws.com for the region stored with the account. The proxy signs each request; only model invoke and converse calls are forwarded.",
	"opencode-go":          "Chat Completions and Responses models. The client still sends its own x-opencode-client and x-opencode-session headers.",
	"opencode-go-messages": "Messages-protocol models. The client still sends its own x-opencode-client and x-opencode-session headers.",
	"opencode-go-gemini":   "Gemini-protocol models. The client still sends its own x-opencode-client and x-opencode-session headers.",
	"elevenlabs":           "The client appends /v1 speech or music paths. Put the client token in its API key field.",
	"ollama":               "Localhost is the proxy machine. Acknowledge HTTP below. No upstream key is substituted; clients still authenticate to the proxy with their client token.",
	"vllm":                 "Enter your server URL. Upstream bearer authentication is optional: remove the substitution if the server has no key.",
	"openai-compatible":    "Enter your server URL. Upstream bearer authentication is optional: remove the substitution if the server has no key.",
}

// usagePresets are routes without a provider type that still serve a token-reporting protocol.
var usagePresets = map[string]bool{"opencode-go-messages": true, "opencode-go-gemini": true}

func presetsFromProfiles(profiles []proxyprofile.Profile) []routePreset {
	out := make([]routePreset, 0, len(profiles))
	for _, p := range profiles {
		// A gated profile has no verified credentialless path; offering it would
		// claim an integration the proxy cannot deliver. An account kind without
		// proxy custody support cannot be selected either.
		if p.Gate != "" || p.Account != "" && !accountKind(p.Account) {
			continue
		}
		preset := routePreset{ID: p.ID, Label: p.Label, Upstream: p.Upstream, Auth: Auth{Header: p.Header, Prefix: p.Prefix}, Note: presetNotes[p.ID], Billing: p.Billing, WebSocket: p.WebSocket, AccountKind: p.Account}
		if p.Billing == "local" {
			preset.Auth = Auth{} // Default bearer client authentication, no upstream key.
		}
		if p.Provider != "" || usagePresets[p.ID] {
			preset.Usage = "auto"
		}
		if p.Account != "" {
			preset.Note = joinNote("Uses a trusted "+p.Account+" account held by the proxy; choose it below. No API key is stored on the route.", preset.Note)
		}
		if p.WebSocket {
			preset.Note = joinNote(preset.Note, "WebSocket upgrades are forwarded for this route.")
		}
		if p.PathSuffix != "" {
			preset.Note = joinNote(preset.Note, "Full-endpoint clients append "+p.PathSuffix+" to the route URL.")
		}
		out = append(out, preset)
	}
	return out
}

func joinNote(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + " " + b
}
