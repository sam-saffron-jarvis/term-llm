package serve

import (
	"github.com/term-llm/term-llm/internal/app/runtime/mcpstart"
	"github.com/term-llm/term-llm/internal/app/serve/mcpweb"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/mcp"
	"net/http"
)

func (s *serveServer) handleLinkedMCP(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	failed := func() { writeOpenAIError(w, 500, "server_error", "MCP settings unavailable; check the host") }
	ctx := r.Context()
	peer, _ := linkeddevices.DeviceFromRequest(r)
	installer := peer.MCPInstaller()
	if s.store == nil {
		failed()
		return
	}
	// GET must not instantiate a runtime or start any configured commands.
	mgr, setting, err := mcpstart.LoadSessionManager(ctx, s.cfgRef, s.store, id, s.cfg.agentName)
	if err != nil {
		failed()
		return
	}
	rt, _ := s.sessionMgr.Get(id)
	live := rt.mcpManagerSnapshot()
	allowed := stringSet(mgr.LinkedDeviceServers(installer))
	if hostManagedMCP(mgr, mcpstart.ParseServerList(setting), allowed) || !linkedActiveMCPAllowed(live, installer, allowed) {
		writeOpenAIError(w, 409, "conflict_error", "This conversation’s MCP selection is managed on the host.")
		return
	}
	if r.Method == http.MethodGet {
		mcpweb.WriteSelection(w, mgr, live, setting, installer, s.cfg.publicURL)
		return
	}
	names, ok := mcpweb.ParseSelection(w, r)
	if !ok {
		return
	}
	denied := func() {
		writeOpenAIError(w, 403, "permission_error", "MCP server is not shared with this conversation")
	}
	if !mcpweb.Allows(names, allowed) {
		denied()
		return
	}
	sess, err := s.store.Get(ctx, id)
	if err != nil {
		failed()
		return
	}
	if sess == nil && !validResponseDraftID(id) {
		writeOpenAIError(w, 404, "not_found_error", "conversation not found")
		return
	}
	if sessionIsDelegatedChild(sess) {
		writeOpenAIError(w, 409, "conflict_error", delegatedSessionWriteMessage)
		return
	}
	rt, err = s.metadataRuntime(ctx, id)
	if err != nil || rt == nil {
		failed()
		return
	}
	if !rt.mu.TryLock() {
		writeOpenAIError(w, 409, "conflict_error", "conversation is busy")
		return
	}
	defer rt.mu.Unlock()
	if rt.hasActiveRun() {
		writeOpenAIError(w, 409, "conflict_error", "conversation is busy")
		return
	}
	if rt.refreshMCPManagerLocked() != nil {
		failed()
		return
	}
	allowed = stringSet(rt.mcpManager.LinkedDeviceServers(installer))
	if !mcpweb.Allows(names, allowed) {
		denied()
		return
	}
	// Never remove (or implicitly start) an owner's non-shared selection.
	// Unconfigured names are stale and are dropped by applying names.
	sess, err = s.store.Get(ctx, id)
	if err != nil {
		failed()
		return
	}
	if (sess != nil && hostManagedMCP(rt.mcpManager, mcpstart.ParseServerList(sess.MCP), allowed)) || hostManagedMCP(rt.mcpManager, mcpstart.ParseServerList(rt.mcpSetting), allowed) || hostManagedMCP(rt.mcpManager, rt.mcpManager.EnabledServers(), allowed) {
		writeOpenAIError(w, 409, "conflict_error", "This conversation's MCP selection is managed on the host.")
		return
	}
	if err := rt.applyMCPSelectionLocked(ctx, names); err != nil {
		failed()
		return
	}
	if err := s.persistSessionMCPSelectionLocked(ctx, id, rt, names); err != nil {
		failed()
		return
	}
	s.publishEvent(serveEventInput{Type: serveEventSessionRuntimeChanged, SessionID: id, Reason: "mcp"})
	mcpweb.WriteSelection(w, rt.mcpManager, rt.mcpManager, rt.mcpSetting, installer, s.cfg.publicURL)
}

// A cached runtime may predate changes to the shared or installed server list.
func linkedActiveMCPAllowed(live *mcp.Manager, installer string, configured map[string]bool) bool {
	if live == nil {
		return true
	}
	enabled := live.EnabledServers()
	return mcpweb.Allows(enabled, configured) && mcpweb.Allows(enabled, stringSet(live.LinkedDeviceServers(installer)))
}

// Bind the peer check to the runtime's run lock, not just the selector. The
// owner may have changed this conversation since the panel was last opened.
func linkedMCPRunGuard(p linkeddevices.DevicePrincipal, rt *serveRuntime, next func(*core.Request) error) func(*core.Request) error {
	if p.DeviceID == "" {
		return next
	}
	return func(req *core.Request) error {
		if err := rt.linkedMCPPolicy().checkDevice(p); err != nil {
			return err
		}
		if next != nil {
			return next(req)
		}
		return nil
	}
}

// hostManagedMCP reports a configured name the peer may not use. Unconfigured
// names (a removed device installation, say) are stale: deselecting them is
// safe and nothing can start them.
func hostManagedMCP(mgr *mcp.Manager, names []string, allowed map[string]bool) bool {
	cfg := mgr.Config()
	for _, name := range names {
		if allowed[name] {
			continue
		}
		if cfg == nil {
			return true
		}
		if _, configured := cfg.Servers[name]; configured {
			return true
		}
	}
	return false
}
