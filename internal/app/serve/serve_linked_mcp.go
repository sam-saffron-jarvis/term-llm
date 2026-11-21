package serve

import (
	"errors"
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
	installer := ""
	if peer.ManageMCP {
		installer = peer.DeviceID
	}
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
	if r.Method == http.MethodGet {
		if hostManagedMCP(mgr, mcpstart.ParseServerList(setting), stringSet(mgr.LinkedDeviceServers(installer))) {
			writeOpenAIError(w, 409, "conflict_error", "This conversation’s MCP selection is managed on the host.")
			return
		}
		mcpweb.WriteSelection(w, mgr, setting, installer, s.cfg.publicURL)
		return
	}
	names, ok := mcpweb.ParseSelection(w, r)
	if !ok {
		return
	}
	denied := func() {
		writeOpenAIError(w, 403, "permission_error", "MCP server is not shared with this conversation")
	}
	if !mcpweb.Allows(names, stringSet(mgr.LinkedDeviceServers(installer))) {
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
	rt, err := s.metadataRuntime(ctx, id)
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
	allowed := stringSet(rt.mcpManager.LinkedDeviceServers(installer))
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
	mcpweb.WriteSelection(w, rt.mcpManager, rt.mcpSetting, installer, s.cfg.publicURL)
}

// Bind the peer check to the runtime's run lock, not just the selector. The
// owner may have changed this conversation since the panel was last opened.
func linkedMCPRunGuard(p linkeddevices.DevicePrincipal, rt *serveRuntime, next func(*core.Request) error) func(*core.Request) error {
	if p.DeviceID == "" {
		return next
	}
	return func(req *core.Request) error {
		if err := rt.checkDeviceMCP(p); err != nil {
			return err
		}
		if next != nil {
			return next(req)
		}
		return nil
	}
}

func (rt *serveRuntime) checkDeviceMCP(p linkeddevices.DevicePrincipal) error {
	if rt.mcpManager == nil {
		return nil
	}
	names, selected := rt.mcpManager.EnabledServers(), mcpstart.ParseServerList(rt.mcpSetting)
	if len(names)+len(selected) == 0 {
		return nil
	}
	installer := ""
	if p.ManageMCP && p.MCPGrantLifetime != nil && p.MCPGrantLifetime.Err() == nil {
		installer = p.DeviceID
	}
	denied := errors.New("This conversation uses MCP servers that are not shared with this device.")
	cfg, err := mcp.LoadConfig()
	if err != nil {
		return denied
	}
	// A selected name missing from disk and memory cannot start; running ones are checked.
	known := rt.mcpManager.Config()
	for _, name := range selected {
		_, onDisk := cfg.Servers[name]
		if onDisk || known == nil {
			names = append(names, name)
		} else if _, inMemory := known.Servers[name]; inMemory {
			names = append(names, name)
		}
	}
	if !mcpweb.Allows(names, stringSet(rt.mcpManager.LinkedDeviceServers(installer))) {
		return denied
	}
	for _, name := range names {
		if !cfg.Servers[name].AvailableToDevice(installer) {
			return denied
		}
	}
	return nil
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
