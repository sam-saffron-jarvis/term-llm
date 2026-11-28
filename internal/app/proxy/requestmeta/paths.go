package requestmeta

import (
	"net/http"
	"net/url"
	"strings"
)

// Split escaped transport segments before decoding model identities. An ARN's
// encoded slash belongs to the model, never to endpoint/policy path matching.
func bedrockModel(endpoint string) (string, bool) {
	if !strings.HasPrefix(endpoint, "/model/") {
		return "", false
	}
	escaped, op, ok := strings.Cut(strings.TrimPrefix(endpoint, "/model/"), "/")
	if !ok || (op != "invoke" && op != "invoke-with-response-stream") {
		return "", false
	}
	model, err := url.PathUnescape(escaped)
	return model, err == nil && safeModel(model)
}
func knownAnthropicModel(model string) bool {
	for _, prefix := range []string{"anthropic.", "us.anthropic.", "eu.anthropic.", "apac.anthropic.", "global.anthropic."} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}
func bedrockModelFromPath(path string) (string, bool) {
	at := strings.LastIndex(path, "/model/")
	if at < 0 {
		return "", false
	}
	return bedrockModel(path[at:])
}

func geminiModel(endpoint string) (string, bool) {
	var tail string
	for _, prefix := range []string{"/models/", "/v1/models/", "/v1beta/models/"} {
		if strings.HasPrefix(endpoint, prefix) {
			tail = strings.TrimPrefix(endpoint, prefix)
			break
		}
	}
	at := strings.LastIndexByte(tail, ':')
	if at <= 0 || strings.Contains(tail, "/") {
		return "", false
	}
	op := tail[at+1:]
	if op != "generateContent" && op != "streamGenerateContent" {
		return "", false
	}
	model, err := url.PathUnescape(tail[:at])
	return model, err == nil && safeModel(model) && !strings.Contains(model, "/")
}

func protocolFor(method, path, endpoint string) string {
	if method != http.MethodPost {
		return ""
	}
	switch endpoint {
	case "/responses", "/v1/responses":
		return "responses"
	case "/chat/completions", "/v1/chat/completions":
		return "chat_completions"
	case "/messages", "/v1/messages":
		return "messages"
	case "/api/chat", "/api/generate":
		return "ollama"
	}
	if path == "/v1internal:generateContent" || path == "/v1internal:streamGenerateContent" {
		return "gemini"
	}
	if _, ok := geminiModel(endpoint); ok {
		return "gemini"
	}
	if _, ok := bedrockModel(endpoint); ok {
		return "bedrock"
	}
	return ""
}
