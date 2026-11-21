package serve

import (
	"github.com/term-llm/term-llm/internal/app/serve/mcpweb"
	"net/http"
)

type mcpRegistrySearcher = mcpweb.RegistrySearcher
type mcpAddResponse = mcpweb.AddResponse
type mcpCatalogueResponse = mcpweb.CatalogueResponse

func (s *serveServer) mcpWeb() mcpweb.Handler {
	return mcpweb.Handler{Registry: s.mcpRegistry, MutationAllowed: s.mcpMutationAllowed}
}
func (s *serveServer) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	s.mcpWeb().Servers(w, r)
}
func (s *serveServer) handleMCPServerByName(w http.ResponseWriter, r *http.Request) {
	s.mcpWeb().ServerByName(w, r)
}
func (s *serveServer) handleMCPCatalogue(w http.ResponseWriter, r *http.Request) {
	s.mcpWeb().Catalogue(w, r)
}
func (s *serveServer) mcpMutationAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.requireAuth && !isLoopbackRemoteAddr(r.RemoteAddr) {
		writeOpenAIError(w, http.StatusForbidden, "permission_error", "MCP servers can only be changed on authenticated or local servers")
		return false
	}
	return true
}
