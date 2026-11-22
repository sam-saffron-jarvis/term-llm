package mcpweb

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/mcp"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
)

func linkedRemoteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && mcpoauth.PublicHTTPS(u)
}

func allowLinkedManagement(w http.ResponseWriter, peer linkeddevices.DevicePrincipal) bool {
	w.Header().Set("Cache-Control", "no-store")
	if peer.MCPInstaller() == "" {
		wire.WriteOpenAIError(w, 403, "permission_error", "MCP management is not granted")
		return false
	}
	return true
}

func (s Handler) installLinkedMCP(w http.ResponseWriter, r *http.Request, installer string) {
	var req struct {
		Kind        string `json:"kind"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		CatalogueID string `json:"catalogue_id"`
		DryRun      bool   `json:"dry_run"`
	}
	body := http.MaxBytesReader(w, r.Body, 8<<10)
	defer body.Close()
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if wire.RequireJSONContentType(r) != nil || dec.Decode(&req) != nil || dec.Decode(&struct{}{}) != io.EOF || (req.Kind != "catalogue" && req.Kind != "url") {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", "choose a public HTTPS server")
		return
	}
	name, cfg, needsInput, err := s.resolveMCPAdd(r.Context(), mcpAddRequest{Kind: req.Kind, Name: req.Name, URL: req.URL, CatalogueID: req.CatalogueID})
	if err != nil || needsInput || !linkedRemoteURL(cfg.URL) || cfg.Command != "" || len(cfg.Headers) > 0 {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", "choose a public HTTPS server without placeholders")
		return
	}
	// Do not copy registry-controlled environment, OAuth secrets or other
	// deferred configuration into a delegated connection.
	disabled := false
	cfg = mcp.ServerConfig{Type: "http", URL: cfg.URL, LinkedInstaller: installer, Sampling: &mcp.SamplingConfig{Enabled: &disabled}}
	writeInstallation(w, AddResponse{Name: name, Transport: cfg.TransportType()}, cfg, req.DryRun)
}
