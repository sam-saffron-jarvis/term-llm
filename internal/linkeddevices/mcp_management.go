package linkeddevices

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

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
func (r *Runtime) cancelMCPLocked(id string) {
	if l, ok := r.mcpGrants[id]; ok {
		l.cancel()
		delete(r.mcpGrants, id)
	}
}

// authorizePeerRequest binds this request (not the connection) to a freshly
// authenticated grant. Management is never part of the ordinary web-v1 scope.
func authorizePeerRequest(req *http.Request, basePath string, grant pairing.Grant, lifetime, management context.Context) error {
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
	*req = *WithDevicePrincipal(req, DevicePrincipal{DeviceID: grant.DeviceID, Scope: grant.Scope, ApprovedBy: grant.ApprovedBy, Lifetime: lifetime, ManageMCP: grant.ManageMCP, MCPGrantLifetime: management})
	return nil
}
