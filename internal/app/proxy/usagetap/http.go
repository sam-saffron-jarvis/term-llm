package usagetap

import (
	"net/http"
	"strings"
)

// Tap observes bytes and finalizes usage, including wrapped/opaque adapters.
type Tap interface {
	Feed([]byte)
	Finish(bool) Usage
}

// ForHTTPResponse selects a response-only tap by wire path/framing, preserving
// the trusted-origin gates for provider-specific accounting adapters.
func ForHTTPResponse(upstream string, adapters bool, resp *http.Response) (Tap, Protocol) {
	path := ""
	if resp.Request != nil && resp.Request.URL != nil {
		path = resp.Request.URL.Path
	}
	// Cost adapters require a trusted origin as well as the endpoint schema.
	if adapters && IsExaSearchJSON(upstream, resp) {
		return &ExaSearchJSON{}, ExaSearch
	}
	if adapters && IsTypeSafeSystemOneJSON(upstream, resp) {
		return New(TypeSafeSystemOne, JSON), TypeSafeSystemOne
	}
	ct := resp.Header.Get("Content-Type")
	if adapters && len(resp.Header.Values("Content-Type")) <= 1 && IsVeniceChatResponse(upstream, resp) {
		return ForVeniceChatCompletions(ct), ChatCompletions
	}
	if adapters && len(resp.Header.Values("Content-Type")) <= 1 && IsMistralChatResponse(upstream, resp) {
		return ForMistralChatCompletions(ct), ChatCompletions
	}
	if strings.EqualFold(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]), "application/vnd.amazon.eventstream") {
		return NewEventStream(), Bedrock
	}
	p := ForResponse(path, ct)
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// WS upgrades usually have no Content-Type. Select only the endpoint schema.
		p = ForResponse(path, "application/json")
		return NewWebSocket(p), p.Protocol()
	}
	if strings.Contains(path, "/model/") && strings.HasSuffix(path, "/invoke") && p.Protocol() == "" {
		return New(Messages, JSON), Bedrock
	}
	return p, p.Protocol()
}

// DiagnosticSnapshot isolates optional diagnostic failures from forwarding and usage.
func DiagnosticSnapshot(observer Tap, enabled bool) (metadata *ResponseMetadata) {
	defer func() {
		if recover() != nil {
			metadata = nil
		}
	}()
	if enabled {
		if tap, ok := observer.(interface {
			ResponseMetadata() *ResponseMetadata
		}); ok {
			return tap.ResponseMetadata()
		}
	}
	return nil
}
