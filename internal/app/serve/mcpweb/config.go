package mcpweb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/mcp"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
)

type Handler struct {
	Registry        RegistrySearcher
	MutationAllowed func(http.ResponseWriter, *http.Request) bool
}

type RegistrySearcher interface {
	Search(context.Context, mcp.SearchOptions) (*mcp.SearchResult, error)
}

func (s Handler) registryForMCP() RegistrySearcher {
	if s.Registry != nil {
		return s.Registry
	}
	return mcp.NewRegistryClient()
}

type mcpCatalogueItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Transport   string `json:"transport"`
	Source      string `json:"source"`
	Official    bool   `json:"official"`
	Installed   bool   `json:"installed"`
	NeedsInput  bool   `json:"needs_input"`
}

type CatalogueResponse struct {
	Servers       []mcpCatalogueItem `json:"servers"`
	RegistryError string             `json:"registry_error,omitempty"`
}

func mcpBundledCatalogue(query string, installed map[string]mcp.ServerConfig) []mcpCatalogueItem {
	var items []mcpCatalogueItem
	seen := make(map[string]bool)
	for _, bundled := range mcp.GetBundledServers() {
		if seen[bundled.Name] {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{bundled.Name, bundled.Description, bundled.Package, bundled.Category}, " ")), query) {
			continue
		}
		seen[bundled.Name] = true
		transport := bundled.PackageType
		if bundled.RemoteURL != "" {
			transport = "remote"
		} else if transport == "" {
			transport = "npm"
		}
		_, exists := installed[bundled.Name]
		items = append(items, mcpCatalogueItem{ID: "bundled:" + bundled.Name, Name: bundled.Name, Title: bundled.Name, Description: bundled.Description, Category: bundled.Category, Transport: transport, Source: "bundled", Official: bundled.Official, Installed: exists})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Official != items[j].Official {
			return items[i].Official
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func mcpRegistryTransport(server *mcp.RegistryServer, cfg mcp.ServerConfig) string {
	if cfg.URL != "" {
		return "remote"
	}
	for _, pkg := range server.Packages {
		if (pkg.RegistryType == "npm" && cfg.Command == "npx") || (pkg.RegistryType == "pypi" && cfg.Command == "uvx") {
			return pkg.RegistryType
		}
	}
	return ""
}

func (s Handler) Catalogue(w http.ResponseWriter, r *http.Request) {
	peer, linked := linkeddevices.DeviceFromRequest(r)
	if linked && !allowLinkedManagement(w, peer) {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		wire.WriteOpenAIError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	cfg, err := mcp.LoadConfig()
	if err != nil {
		wire.WriteOpenAIError(w, 500, "server_error", "MCP configuration unavailable")
		return
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	response := CatalogueResponse{Servers: mcpBundledCatalogue(query, cfg.Servers)}
	if linked {
		entries := []mcpCatalogueItem{}
		for _, item := range response.Servers {
			if item.Transport == "remote" {
				entries = append(entries, item)
			}
		}
		response.Servers = entries
	}
	if query != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		result, searchErr := s.registryForMCP().Search(ctx, mcp.SearchOptions{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: 30})
		if searchErr != nil {
			response.RegistryError = searchErr.Error()
			if linked {
				response.RegistryError = "Registry unavailable"
			}
		} else if result != nil {
			seen := make(map[string]bool)
			for _, item := range response.Servers {
				seen[item.Name] = true
			}
			for _, wrapper := range result.Servers {
				server := &wrapper.Server
				config, needsInput := server.ToServerConfig()
				if linked && (config.URL == "" || needsInput || !linkedRemoteURL(config.URL)) {
					continue
				}
				if config.Command == "" && config.URL == "" {
					continue
				}
				name := mcp.DeriveNameFromRegistry(server, server.Name)
				if seen[name] {
					continue
				}
				seen[name] = true
				_, installed := cfg.Servers[name]
				response.Servers = append(response.Servers, mcpCatalogueItem{ID: "registry:" + server.Name, Name: name, Title: server.DisplayName(), Description: server.Description, Transport: mcpRegistryTransport(server, config), Source: "registry", Installed: installed, NeedsInput: needsInput})
				if len(response.Servers) >= 40 {
					break
				}
			}
		}
	}
	if len(response.Servers) > 40 {
		response.Servers = response.Servers[:40]
	}
	if response.Servers == nil {
		response.Servers = []mcpCatalogueItem{}
	}
	wire.WriteJSON(w, http.StatusOK, response)
}

type mcpAddRequest struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Command     string            `json:"command"`
	Env         map[string]string `json:"env"`
	CatalogueID string            `json:"catalogue_id"`
	Config      mcp.ServerConfig  `json:"config"`
	DryRun      bool              `json:"dry_run"`
}

type AddResponse struct {
	Name       string `json:"name"`
	Transport  string `json:"transport"`
	ConfigPath string `json:"config_path"`
	NeedsInput bool   `json:"needs_input"`
	Exists     bool   `json:"exists,omitempty"`
}

func resolveMCPURL(raw string, headers map[string]string) (string, mcp.ServerConfig, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", mcp.ServerConfig{}, fmt.Errorf("invalid MCP URL")
	}
	return mcp.DeriveNameFromURL(u), mcp.ServerConfig{Type: "http", URL: raw, Headers: headers}, nil
}

func (s Handler) resolveMCPAdd(ctx context.Context, req mcpAddRequest) (string, mcp.ServerConfig, bool, error) {
	var name string
	var cfg mcp.ServerConfig
	var needsInput bool
	switch req.Kind {
	case "url":
		var err error
		name, cfg, err = resolveMCPURL(req.URL, req.Headers)
		if err != nil {
			return "", cfg, false, err
		}
	case "command":
		argv, err := mcp.SplitCommandLine(req.Command)
		if err != nil {
			return "", cfg, false, fmt.Errorf("invalid MCP command: %w", err)
		}
		name, cfg = mcp.DeriveNameFromCommand(argv), mcp.ServerConfig{Command: argv[0], Args: argv[1:], Env: req.Env}
	case "config":
		cfg = req.Config
		if req.Name == "" {
			return "", cfg, false, fmt.Errorf("config requires a name")
		}
	case "catalogue":
		var err error
		name, cfg, needsInput, err = s.resolveMCPCatalogue(ctx, req.CatalogueID)
		if err != nil {
			return "", cfg, false, err
		}
		if cfg.TransportType() == "stdio" && len(req.Env) > 0 {
			if cfg.Env == nil {
				cfg.Env = make(map[string]string)
			}
			for key, value := range req.Env {
				cfg.Env[key] = value
			}
		}
	default:
		return "", cfg, false, fmt.Errorf("invalid MCP server kind %q", req.Kind)
	}
	if req.Name != "" {
		name = req.Name
	}
	if err := mcp.ValidateServerName(name); err != nil {
		return "", cfg, false, err
	}
	if err := cfg.Validate(); err != nil {
		return "", cfg, false, fmt.Errorf("invalid MCP server config: %w", err)
	}
	return name, cfg, needsInput, nil
}

func (s Handler) resolveMCPCatalogue(ctx context.Context, catalogueID string) (string, mcp.ServerConfig, bool, error) {
	id, value, ok := strings.Cut(catalogueID, ":")
	invalid := fmt.Errorf("unknown catalogue id %q", catalogueID)
	if !ok || value == "" {
		return "", mcp.ServerConfig{}, false, invalid
	}
	if id == "bundled" {
		for _, b := range mcp.GetBundledServers() {
			if b.Name == value {
				return b.Name, b.ToServerConfig(), false, nil
			}
		}
		return "", mcp.ServerConfig{}, false, invalid
	}
	if id != "registry" {
		return "", mcp.ServerConfig{}, false, invalid
	}
	searchCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	result, err := s.registryForMCP().Search(searchCtx, mcp.SearchOptions{Query: value, Limit: 100})
	if err != nil {
		return "", mcp.ServerConfig{}, false, fmt.Errorf("search MCP registry: %w", err)
	}
	if result != nil {
		for _, wrapper := range result.Servers {
			if wrapper.Server.Name != value {
				continue
			}
			cfg, needsInput := wrapper.Server.ToServerConfig()
			if cfg.Command == "" && cfg.URL == "" {
				break
			}
			return mcp.DeriveNameFromRegistry(&wrapper.Server, value), cfg, needsInput, nil
		}
	}
	return "", mcp.ServerConfig{}, false, invalid
}

func (s Handler) Servers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		wire.WriteOpenAIError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	if peer, linked := linkeddevices.DeviceFromRequest(r); linked {
		if allowLinkedManagement(w, peer) {
			s.installLinkedMCP(w, r, peer.DeviceID)
		}
		return
	}
	if !s.MutationAllowed(w, r) {
		return
	}
	if err := wire.RequireJSONContentType(r); err != nil {
		wire.WriteOpenAIError(w, http.StatusUnsupportedMediaType, "invalid_request_error", err.Error())
		return
	}
	var req mcpAddRequest
	if err := wire.DecodeJSONBody(r, &req); err != nil {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", fmt.Sprintf("decode MCP request: %v", err))
		return
	}
	name, cfg, needsInput, err := s.resolveMCPAdd(r.Context(), req)
	if err != nil {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", err.Error())
		return
	}
	path, err := mcp.DefaultConfigPath()
	if err != nil {
		wire.WriteOpenAIError(w, 500, "server_error", err.Error())
		return
	}
	response := AddResponse{Name: name, Transport: cfg.TransportType(), ConfigPath: path, NeedsInput: needsInput}
	writeInstallation(w, response, cfg, req.DryRun)
}

func writeInstallation(w http.ResponseWriter, response AddResponse, cfg mcp.ServerConfig, dryRun bool) {
	if dryRun {
		current, err := mcp.LoadConfig()
		if err != nil {
			wire.WriteOpenAIError(w, 500, "server_error", "MCP configuration unavailable")
			return
		}
		_, response.Exists = current.Servers[response.Name]
		wire.WriteJSON(w, 200, response)
		return
	}
	duplicate := errors.New("MCP server exists")
	err := mcp.UpdateConfig(func(current *mcp.Config) error {
		if _, exists := current.Servers[response.Name]; exists {
			return duplicate
		}
		current.AddServer(response.Name, cfg)
		return nil
	})
	if errors.Is(err, duplicate) {
		wire.WriteOpenAIError(w, 409, "conflict_error", fmt.Sprintf("MCP server %q already exists", response.Name))
		return
	}
	if err != nil {
		wire.WriteOpenAIError(w, 500, "server_error", "could not install MCP server")
		return
	}
	wire.WriteJSON(w, 201, response)
}

func (s Handler) ServerByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		wire.WriteOpenAIError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	peer, linked := linkeddevices.DeviceFromRequest(r)
	if linked {
		if !allowLinkedManagement(w, peer) {
			return
		}
	} else if !s.MutationAllowed(w, r) {
		return
	}
	// URL.Path is decoded by net/http; reject nested paths. Existing entries may
	// predate ValidateServerName, so any configured name can be removed.
	name := strings.TrimPrefix(r.URL.Path, "/v1/mcp/servers/")
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		wire.WriteOpenAIError(w, 400, "invalid_request_error", "invalid MCP server name")
		return
	}
	missing := errors.New("MCP server not found")
	var removed mcp.ServerConfig
	accountInUse := false
	err := mcp.UpdateConfig(func(cfg *mcp.Config) error {
		var exists bool
		removed, exists = cfg.Servers[name]
		if !exists || (linked && removed.LinkedInstaller != peer.DeviceID) {
			return missing
		}
		cfg.RemoveServer(name)
		for _, other := range cfg.Servers {
			accountInUse = accountInUse || (other.LinkedInstaller == removed.LinkedInstaller && other.URL == removed.URL)
		}
		return nil
	})
	if errors.Is(err, missing) {
		if linked {
			wire.WriteOpenAIError(w, 403, "permission_error", "not an installation managed by this device")
			return
		}
		wire.WriteOpenAIError(w, 404, "not_found_error", fmt.Sprintf("MCP server %q not found", name))
		return
	}
	if err != nil {
		wire.WriteOpenAIError(w, 500, "server_error", "could not remove MCP server")
		return
	}
	// A device's own removal also ends its sign-in; owner removal keeps undo intact.
	if linked && !accountInUse && mcpoauth.ForgetInstallation(r.Context(), peer.DeviceID, removed.URL) != nil {
		wire.WriteOpenAIError(w, 500, "server_error", "server removed, but its sign-in could not be deleted")
		return
	}
	result := map[string]any{"name": name}
	if !linked {
		result["config"] = removed // Owner-only undo payload.
	}
	wire.WriteJSON(w, 200, result)
}
