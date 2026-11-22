package serve

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/term-llm/term-llm/internal/app/runtime/mcpstart"
	"github.com/term-llm/term-llm/internal/app/serve/mcpweb"
	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/mcp"
)

// linkedMCPPolicy is immutable selection state, not cached device authority.
type linkedMCPPolicy struct {
	manager   *mcp.Manager
	selection string
}

func (rt *serveRuntime) linkedMCPPolicy() *linkedMCPPolicy {
	return &linkedMCPPolicy{manager: rt.mcpManager, selection: rt.mcpSetting}
}

func linkedRuntimePrincipal(ctx context.Context) linkeddevices.DevicePrincipal {
	p, _ := linkeddevices.DeviceFromRequest((&http.Request{}).WithContext(ctx))
	return p
}

func linkedLiveContext(ctx context.Context, live *liveSession) context.Context {
	if live == nil || live.linkedOwner == nil {
		return ctx
	}
	return linkeddevices.WithDevicePrincipal((&http.Request{}).WithContext(ctx), live.linkedOwner.Principal()).Context()
}

// checkLinkedMCPPolicy uses the same policy as the final, under-run-lock gate.
// A snapshot contains only immutable selection state and a concurrency-safe
// manager. Never read the live runtime's mcpSetting without owning rt.mu.
func checkLinkedMCPPolicy(ctx context.Context, policy *linkedMCPPolicy) error {
	p := linkedRuntimePrincipal(ctx)
	if p.DeviceID == "" {
		return nil
	}
	if policy == nil {
		return errServeSessionBusy
	}
	return policy.checkDevice(p)
}

func (policy *linkedMCPPolicy) checkDevice(p linkeddevices.DevicePrincipal) error {
	if policy.manager == nil {
		return nil
	}
	names, selected := policy.manager.EnabledServers(), mcpstart.ParseServerList(policy.selection)
	if len(names)+len(selected) == 0 {
		return nil
	}
	installer := p.MCPInstaller()
	denied := errors.New("This conversation uses MCP servers that are not shared with this device.")
	cfg, err := mcp.LoadConfig()
	if err != nil {
		return denied
	}
	// A selected name missing from disk and memory cannot start; running ones are checked.
	known := policy.manager.Config()
	for _, name := range selected {
		_, onDisk := cfg.Servers[name]
		if onDisk || known == nil {
			names = append(names, name)
		} else if _, inMemory := known.Servers[name]; inMemory {
			names = append(names, name)
		}
	}
	if !mcpweb.Allows(names, stringSet(policy.manager.LinkedDeviceServers(installer))) {
		return denied
	}
	for _, name := range names {
		if !cfg.Servers[name].AvailableToDevice(installer) {
			return denied
		}
	}
	return nil
}

// checkLinkedSessionMCP is deliberately non-starting. Restoring persisted MCP
// is preparation, not execution: authorization must precede that side effect.
func (s *serveServer) checkLinkedSessionMCP(ctx context.Context, sessionID string) error {
	if linkedRuntimePrincipal(ctx).DeviceID == "" {
		return nil
	}
	mgr, setting, err := mcpstart.LoadSessionManager(ctx, s.cfgRef, s.store, sessionID, s.cfg.agentName)
	if err != nil {
		return err
	}
	return checkLinkedMCPPolicy(ctx, &linkedMCPPolicy{manager: mgr, selection: setting})
}

// linkedMCPPreparationCheck checks the actual resolved defaults once, before
// preparation can start any MCP connection. It uses the manager that startup
// will use, rather than resolving mutable agent defaults a second time.
func linkedMCPPreparationCheck(ctx context.Context) func(*mcpstart.PreparedSelection) error {
	if linkedRuntimePrincipal(ctx).DeviceID == "" {
		return nil
	}
	return func(selection *mcpstart.PreparedSelection) error {
		return checkLinkedMCPPolicy(ctx, &linkedMCPPolicy{manager: selection.Manager, selection: selection.Durable})
	}
}

// authorizeLinkedGoalEdit pins either the idle runtime or its active turn while
// the caller mutates the goal. It never waits for rt.mu, which a turn holds for
// its entire duration. The interrupt lock fences turn teardown/replacement; the
// snapshot carries selection, not a cached authorization for another principal.
func (s *serveServer) authorizeLinkedGoalEdit(ctx context.Context, sessionID string) (func(), error) {
	noop := func() {}
	if linkedRuntimePrincipal(ctx).DeviceID == "" {
		return noop, nil
	}
	if s.sessionMgr == nil {
		return noop, s.checkLinkedSessionMCP(ctx, sessionID)
	}
	m := s.sessionMgr
	operation := m.sessionOperation(sessionID)
	if !operation.TryLock() {
		return nil, errServeSessionBusy
	}
	m.reserved.Store(sessionID, struct{}{})
	releaseIdentity := func() {
		m.reserved.Delete(sessionID)
		operation.Unlock()
	}
	m.mu.Lock()
	rt, creating, closed := m.sessions[sessionID], m.creating[sessionID] != nil, m.closed
	m.mu.Unlock()
	if creating || closed {
		releaseIdentity()
		return nil, errServeSessionBusy
	}
	if err := s.checkLinkedSessionMCP(ctx, sessionID); err != nil {
		releaseIdentity()
		return nil, err
	}
	if rt == nil {
		return releaseIdentity, nil
	}
	if rt.mu.TryLock() {
		if err := checkLinkedMCPPolicy(ctx, rt.linkedMCPPolicy()); err != nil {
			rt.mu.Unlock()
			releaseIdentity()
			return nil, err
		}
		return func() { rt.mu.Unlock(); releaseIdentity() }, nil
	}
	rt.interruptMu.Lock()
	var policy *linkedMCPPolicy
	if rt.activeInterrupt != nil {
		policy = rt.activeInterrupt.mcpPolicy
	}
	if err := checkLinkedMCPPolicy(ctx, policy); err != nil {
		rt.interruptMu.Unlock()
		releaseIdentity()
		return nil, err
	}
	return func() { rt.interruptMu.Unlock(); releaseIdentity() }, nil
}

func (s *serveServer) ensureRuntimeMCPForSession(ctx context.Context, sessionID string, rt *serveRuntime) error {
	if s == nil || rt == nil {
		return nil
	}
	if rt.hasActiveRun() || !rt.mu.TryLock() {
		return errServeSessionBusy
	}
	defer rt.mu.Unlock()
	if rt.hasActiveRun() {
		return errServeSessionBusy
	}
	if err := checkLinkedMCPPolicy(ctx, rt.linkedMCPPolicy()); err != nil {
		return err
	}
	if err := s.checkLinkedSessionMCP(ctx, sessionID); err != nil {
		return err
	}
	if err := s.applyPersistedMCPSelectionLocked(ctx, sessionID, rt); err != nil {
		log.Printf("[serve] restore MCP selection failed for %s: %v", sessionID, err)
	}
	return nil
}

// linkedRuntimeForRequest is the Live preparation boundary. HTTP responses
// perform the same preflight during admission, before UI preparation can run.
func (s *serveServer) linkedRuntimeForRequest(ctx context.Context, sessionID string) (*serveRuntime, bool, error) {
	if err := s.checkLinkedSessionMCP(ctx, sessionID); err != nil {
		return nil, false, err
	}
	return s.runtimeForRequest(ctx, sessionID)
}
