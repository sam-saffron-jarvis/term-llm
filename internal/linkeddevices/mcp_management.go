package linkeddevices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/term-llm/term-llm/internal/mcp"
	"github.com/term-llm/term-llm/internal/pairing"
)

const managementServerName = `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`

var (
	managedServerPath      = regexp.MustCompile(`^/v1/mcp/servers/` + managementServerName + `$`)
	managedSessionAuthPath = regexp.MustCompile(`^/v1/sessions/[A-Za-z0-9_-]+/mcp/` + managementServerName + `/oauth/(start|cancel)$`)
	// Flow IDs are 24 random bytes encoded in raw base64url.
	managedFlowPath = regexp.MustCompile(`^/v1/mcp/oauth/flows/[A-Za-z0-9_-]{32}$`)
)

func mcpManagementRoute(method, path string) bool {
	switch method {
	case http.MethodGet:
		return path == "/v1/mcp/catalogue" || managedFlowPath.MatchString(path)
	case http.MethodPost:
		return path == "/v1/mcp/servers" || managedSessionAuthPath.MatchString(path)
	case http.MethodDelete:
		return managedServerPath.MatchString(path)
	}
	return false
}

type mcpLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// The same lock guards grant mutation, lookup and lifetime creation. A start
// already admitted at revocation still holds the old, canceled context.
func (r *Runtime) mcpLifetimeLocked(g pairing.Grant) context.Context {
	if !g.ManageMCP {
		return nil
	}
	if l, ok := r.mcpGrants[g.DeviceID]; ok {
		return l.ctx
	}
	if r.mcpGrants == nil {
		r.mcpGrants = make(map[string]mcpLifetime)
	}
	ctx, cancel := context.WithCancel(r.sharingCtx)
	r.mcpGrants[g.DeviceID] = mcpLifetime{ctx, cancel}
	return ctx
}

// Management requests hold the device's read lock while they run; disposal
// takes the write lock, so no admitted install or sign-in outlives it.
func (r *Runtime) mcpOpsLocked(id string) *sync.RWMutex {
	if r.mcpOps == nil {
		r.mcpOps = make(map[string]*sync.RWMutex)
	}
	if r.mcpOps[id] == nil {
		r.mcpOps[id] = new(sync.RWMutex)
	}
	return r.mcpOps[id]
}

// disposeMCP removes a device's MCP installations and accounts. The caller
// holds mcpDisposeMu (not mu) and has already ended the grant lifetime.
func (r *Runtime) disposeMCP(ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	r.mu.Lock()
	ops := make([]*sync.RWMutex, len(ids))
	for i, id := range ids {
		ops[i] = r.mcpOpsLocked(id)
	}
	r.mu.Unlock()
	for _, lock := range ops {
		lock.Lock()
		defer lock.Unlock()
	}
	err := mcp.DisposeInstallers(ids...)
	if err != nil {
		r.setWarning(msgMCPDisposePending)
	}
	return err
}

// disposeUnmanagedMCP repairs an interrupted disposal at startup: only active
// grants with management may own MCP installations or accounts.
func (r *Runtime) disposeUnmanagedMCP() {
	grants, err := r.grants.List()
	if err != nil {
		return
	}
	var ids []string
	for _, g := range grants {
		if g.RevokedAt != nil || !g.ManageMCP {
			ids = append(ids, g.DeviceID)
		}
	}
	if len(ids) > 0 && mcp.DisposeInstallers(ids...) != nil {
		r.setWarning(msgMCPDisposePending)
	}
}

// resetMCP retains revoked grants as a durable cleanup journal until disposal
// succeeds. Open the on-disk store even when loadState stopped at a target or
// broker mismatch and never populated r.grants. Sharing is already stopped and
// the caller holds lifecycleMu and mcpDisposeMu, but not mu.
func (r *Runtime) resetMCP() error {
	grants, err := pairing.OpenGrantStore(filepath.Join(r.dir, "devices.json"))
	if err != nil {
		return fmt.Errorf("open grants for reset: %w", err)
	}
	devices, err := grants.List()
	if err != nil {
		return fmt.Errorf("list grants for reset: %w", err)
	}
	ids := make([]string, 0, len(devices))
	for _, g := range devices {
		if err := grants.Revoke(g.DeviceID, r.opts.Now()); err != nil {
			return fmt.Errorf("revoke grant for reset: %w", err)
		}
		ids = append(ids, g.DeviceID)
	}
	if err := r.disposeMCP(ids...); err != nil {
		return fmt.Errorf("dispose MCP installations for reset: %w", err)
	}
	return nil
}

func (r *Runtime) cancelMCPLocked(id string) {
	if l, ok := r.mcpGrants[id]; ok {
		l.cancel()
		delete(r.mcpGrants, id)
	}
}

// authorizePeerRequest binds this request (not the connection) to a freshly
// authenticated grant. Management is never part of the ordinary web-v1 scope.
func authorizePeerRequest(req *http.Request, basePath string, grant pairing.Grant, lifetime, management context.Context, ops *sync.RWMutex) error {
	escaped := req.URL.EscapedPath() != req.URL.Path
	if err := prepareMountedRequest(req, basePath); err != nil {
		return err
	}
	allowed := RouteAllowed(req.Method, req.URL.Path)
	if mcpManagementRoute(req.Method, req.URL.Path) {
		allowed = !escaped && grant.ManageMCP
	}
	if !allowed || escaped && strings.HasPrefix(req.URL.Path, "/v1/live") {
		return errors.New("route is not available to paired devices")
	}
	*req = *WithDevicePrincipal(req, DevicePrincipal{DeviceID: grant.DeviceID, Scope: grant.Scope, ApprovedBy: grant.ApprovedBy, Lifetime: lifetime, ManageMCP: grant.ManageMCP, MCPGrantLifetime: management, mcpOps: ops})
	return nil
}
