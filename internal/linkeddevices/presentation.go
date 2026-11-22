package linkeddevices

import (
	"bytes"
	"net/http"
)

// ShapeCapabilities replaces owner-only feature claims with the deliberately
// narrow web-v1 peer profile and marks the response private when applied.
func ShapeCapabilities(response http.ResponseWriter, request *http.Request, payload map[string]any) {
	principal, ok := DeviceFromRequest(request)
	if !ok {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	payload["shell"] = map[string]any{"enabled": false, "reason": "not available through Linked devices"}
	// Keep the provider's readiness, transport and delegation modes intact.
	live, _ := payload["live"].(map[string]any)
	voice, _ := live["enabled"].(bool)
	// Preserve the host attachment policy and reference format; the peer client
	// uploads in bounded chunks rather than through the ordinary upload route.
	payload["linked_devices"] = map[string]any{
		"active": true, "scope": principal.Scope, "owner_controls": false,
		"transcript_sharing": false, "uploads": true, "voice": voice,
		"mcp_manage": principal.ManageMCP,
		"mcp":        true, "goals": true,
		"push_notifications": false, "device_tools": true,
	}
}

// PeerIndexHTML adds a request-local authentication marker without mutating the
// cached owner shell.
func PeerIndexHTML(request *http.Request, body []byte) []byte {
	principal, ok := DeviceFromRequest(request)
	if !ok {
		return body
	}
	snippet := []byte(`<script>window.TERM_LLM_AUTH_MODE="linked-devices";window.TERM_LLM_LINKED_DEVICES_SCOPE="` + principal.Scope + `";</script></head>`)
	return bytes.Replace(body, []byte("</head>"), snippet, 1)
}
