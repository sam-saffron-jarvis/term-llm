package cmd

import (
	"strings"
	"testing"
)

// TestServeWebRTC_HeadSnippetAbsent verifies that renderIndexHTML does not
// inject WebRTC globals when webrtcHeadSnippet is empty (default).
func TestServeWebRTC_HeadSnippetAbsent(t *testing.T) {
	s := &serveServer{
		cfg:               serveServerConfig{basePath: "/ui"},
		webrtcHeadSnippet: "",
	}
	html := string(s.renderIndexHTML())
	if strings.Contains(html, "__WEBRTC_ENABLED__") {
		t.Error("renderIndexHTML should not contain __WEBRTC_ENABLED__ when snippet is empty")
	}
	if strings.Contains(html, "__WEBRTC_SIGNALING_URL__") {
		t.Error("renderIndexHTML should not contain __WEBRTC_SIGNALING_URL__ when snippet is empty")
	}
}

// TestServeWebRTC_InjectsHeadSnippet verifies that a non-empty webrtcHeadSnippet
// is embedded in the rendered HTML.
func TestServeWebRTC_InjectsHeadSnippet(t *testing.T) {
	snippet := `<script>window.__WEBRTC_ENABLED__=true;window.__WEBRTC_SIGNALING_URL__="https://relay.example.com/webrtc";</script>`
	s := &serveServer{
		cfg:               serveServerConfig{basePath: "/ui"},
		webrtcHeadSnippet: snippet,
	}
	html := string(s.renderIndexHTML())
	if !strings.Contains(html, "__WEBRTC_ENABLED__") {
		t.Error("renderIndexHTML should contain __WEBRTC_ENABLED__ when snippet is set")
	}
	if !strings.Contains(html, "relay.example.com") {
		t.Error("renderIndexHTML should contain the signaling URL when snippet is set")
	}
}
