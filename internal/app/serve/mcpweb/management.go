package mcpweb

import (
	"encoding/json"
	"errors"
	"github.com/term-llm/term-llm/internal/app/serve/wire"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/mcp"
)

func linkedRemoteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && mcpoauth.PublicHTTPS(u)
}

func (s Handler) handleLinkedMCPManagement(w http.ResponseWriter, r *http.Request, peer linkeddevices.DevicePrincipal, action string) {
	w.Header().Set("Cache-Control", "no-store")
	if !peer.ManageMCP {
		wire.WriteOpenAIError(w, 403, "permission_error", "MCP management is not granted")
		return
	}
	switch {
	case action == "install" && r.Method == http.MethodPost:
		s.installLinkedMCP(w, r, peer.DeviceID)
	case action == "remove" && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(r.URL.Path, "/v1/mcp/servers/")
		missing := errors.New("not device-managed")
		err := mcp.UpdateConfig(func(cfg *mcp.Config) error {
			if cfg.Servers[name].LinkedInstaller != peer.DeviceID || strings.Contains(name, "/") {
				return missing
			}
			cfg.RemoveServer(name)
			return nil
		})
		if errors.Is(err, missing) {
			wire.WriteOpenAIError(w, 403, "permission_error", "not an installation managed by this device")
			return
		}
		if err != nil {
			wire.WriteOpenAIError(w, 500, "server_error", "could not remove server")
			return
		}
		wire.WriteJSON(w, 200, map[string]string{"name": name})
	default:
		wire.WriteOpenAIError(w, 405, "invalid_request_error", "method not allowed")
	}
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
	result := AddResponse{Name: name, Transport: cfg.TransportType()}
	if req.DryRun {
		current, err := mcp.LoadConfig()
		if err != nil {
			wire.WriteOpenAIError(w, 500, "server_error", "configuration unavailable")
			return
		}
		_, result.Exists = current.Servers[name]
		wire.WriteJSON(w, 200, result)
		return
	}
	exists := errors.New("already configured")
	err = mcp.UpdateConfig(func(c *mcp.Config) error {
		if _, ok := c.Servers[name]; ok {
			return exists
		}
		c.AddServer(name, cfg)
		return nil
	})
	if errors.Is(err, exists) {
		wire.WriteOpenAIError(w, 409, "conflict_error", "server is already configured")
		return
	}
	if err != nil {
		wire.WriteOpenAIError(w, 500, "server_error", "could not install server")
		return
	}
	wire.WriteJSON(w, 201, result)
}
