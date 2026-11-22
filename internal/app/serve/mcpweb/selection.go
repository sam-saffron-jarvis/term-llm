package mcpweb

import (
	"encoding/json"
	"github.com/term-llm/term-llm/internal/app/serve/wire"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/term-llm/term-llm/internal/app/runtime/mcpstart"
	"github.com/term-llm/term-llm/internal/mcp"
)

// Deliberately not the owner view: never serialize config, OAuth metadata,
// discovery diagnostics or errors returned by a host process.
type linkedMCPServer struct {
	Name               string `json:"name"`
	Status             string `json:"status"`
	CanSignIn          bool   `json:"can_sign_in,omitempty"`
	AuthState          string `json:"auth_state,omitempty"`
	HostCallbackNeeded bool   `json:"host_callback_needed,omitempty"`
	CanRemove          bool   `json:"can_remove,omitempty"`
}

func WriteSelection(w http.ResponseWriter, mgr, live *mcp.Manager, setting, installer, publicURL string) {
	selected := mcpstart.ParseServerList(setting)
	if live != nil {
		selected = append(selected, live.EnabledServers()...)
	}
	enabled := []string{}
	servers := []linkedMCPServer{}
	config := mgr.Config()
	loginAvailable := PublicCallback(publicURL)
	for _, name := range mgr.LinkedDeviceServers(installer) {
		status := mcp.StatusStopped
		if live != nil {
			status, _ = live.ServerStatus(name)
		}
		if slices.Contains(selected, name) {
			enabled = append(enabled, name)
		}
		cfg := config.Servers[name]
		own := installer != "" && cfg.LinkedInstaller == installer
		view := linkedMCPServer{Name: name, Status: string(status), CanRemove: own}
		if own && cfg.URL != "" {
			auth := mcpoauth.ForInstaller(installer).Status(cfg.URL)
			view.AuthState = string(auth.State)
			view.CanSignIn = loginAvailable && auth.CanSignIn
			view.HostCallbackNeeded = !loginAvailable && status == mcp.StatusAuthRequired
		}
		servers = append(servers, view)
	}
	wire.WriteJSON(w, http.StatusOK, map[string]any{"servers": servers, "enabled": enabled})
}

func ParseSelection(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	body := http.MaxBytesReader(w, r.Body, 16<<10)
	defer body.Close()
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	var req struct {
		Enabled []string `json:"enabled"`
	}
	if dec.Decode(&req) != nil || req.Enabled == nil || len(req.Enabled) > 128 || dec.Decode(&struct{}{}) != io.EOF {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", "expected a list of enabled MCP server names")
		return nil, false
	}
	return req.Enabled, true
}

func Allows(names []string, available map[string]bool) bool {
	for _, name := range names {
		if !available[name] {
			return false
		}
	}
	return true
}

func PublicCallback(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && mcpoauth.PublicHTTPS(u) && u.RawQuery == ""
}
func WriteFlow(w http.ResponseWriter, status int, flow *mcpoauth.Flow) {
	view := *flow
	if view.Error != "" {
		view.Error = "Authorization was not completed"
	}
	if view.AuthorizationURL != "" {
		u, err := url.Parse(view.AuthorizationURL)
		if err != nil || !mcpoauth.PublicHTTPS(u) {
			wire.WriteOpenAIError(w, 502, "oauth_error", "provider did not supply a public HTTPS sign-in link")
			return
		}
	}
	wire.WriteJSON(w, status, view)
}
