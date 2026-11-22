package linkeddevices

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/term-llm/term-llm/internal/pairing"
)

type devicePrincipalKey struct{}

// DevicePrincipal identifies a grant that was verified by the linked-devices
// transport. It deliberately carries no owner authority.
type DevicePrincipal struct {
	ApprovedBy *pairing.Person
	DeviceID   string
	Scope      string
	ManageMCP  bool // Owner-delegated MCP management, never general owner authority.
	// MCPGrantLifetime is non-nil only for a verified manager. Unlike Lifetime,
	// it survives peer reconnects but ends on permission removal, revoke or stop.
	MCPGrantLifetime context.Context
	// Lifetime is the trusted peer connection's context, not an HTTP request's.
	// It is cancelled on revocation, disconnect, replacement and host shutdown.
	Lifetime context.Context
	mcpOps   *sync.RWMutex // held for reading by management requests
}

// MCPInstaller returns the installer namespace only while the management grant is live.
func (p DevicePrincipal) MCPInstaller() string {
	if p.ManageMCP && p.MCPGrantLifetime != nil && p.MCPGrantLifetime.Err() == nil {
		return p.DeviceID
	}
	return ""
}

// WithDevicePrincipal marks a request as admitted by the peer ingress. It is
// intended only for Runtime's in-process dispatcher.
func WithDevicePrincipal(r *http.Request, principal DevicePrincipal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), devicePrincipalKey{}, principal))
}

// DeviceFromRequest returns the verified linked-device principal, if any.
func DeviceFromRequest(r *http.Request) (DevicePrincipal, bool) {
	principal, ok := r.Context().Value(devicePrincipalKey{}).(DevicePrincipal)
	return principal, ok && principal.DeviceID != "" && principal.Scope == ScopeWebV1
}

// AdmitDevice lets an already verified device pass the normal browser auth
// gate. Owner handlers must still reject device principals explicitly.
func AdmitDevice(next, existing http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := DeviceFromRequest(r); ok {
			next(w, r)
			return
		}
		existing(w, r)
	}
}

// IdentityRequestTrusted reports whether a health request may receive node
// identity. Linked devices are independently trusted. In passkey mode only an
// authenticated passkey session is trusted; bearer matching is exclusive to
// bearer mode, preserving the serve authentication mode's precedence.
func IdentityRequestTrusted(r *http.Request, requireAuth bool, bearerToken string, passkeyMode, passkeyAuthenticated bool) bool {
	if _, ok := DeviceFromRequest(r); ok {
		return true
	}
	if passkeyMode {
		return passkeyAuthenticated
	}
	if !requireAuth {
		return true
	}
	if bearerToken == "" {
		return false
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(bearerToken)) == 1
}

const ScopeWebV1 = "web-v1"

var errOutsideBasePath = errors.New("request is outside the shared mount")

// prepareMountedRequest validates the advertised browser mount and strips it
// exactly once. The peer sends browser-visible paths (for example
// /chat/v1/sessions), while serve's inner mux expects /v1/sessions.
func prepareMountedRequest(request *http.Request, basePath string) error {
	if basePath == "" || basePath == "/" {
		return nil
	}
	escaped := request.URL.EscapedPath()
	if escaped != basePath && !strings.HasPrefix(escaped, basePath+"/") {
		return errOutsideBasePath
	}
	path := request.URL.Path
	if path != basePath && !strings.HasPrefix(path, basePath+"/") {
		return errOutsideBasePath
	}
	path = strings.TrimPrefix(path, basePath)
	if path == "" {
		path = "/"
	}
	request.URL.Path = path
	request.URL.RawPath = ""
	request.RequestURI = path
	if request.URL.RawQuery != "" {
		request.RequestURI += "?" + request.URL.RawQuery
	}
	return nil
}

// RouteAllowed is the positive linked-device route policy. New server routes
// remain denied until deliberately classified here and in the route audit test.
func RouteAllowed(method, path string) bool {
	method = strings.ToUpper(method)
	if strings.HasPrefix(path, "/v1/live") {
		return liveRouteAllowed(method, path)
	}
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if method == http.MethodOptions || strings.HasPrefix(path, "/v1/linked-devices") {
		return false
	}
	if worktreeRouteAllowed(method, path) {
		return true
	}
	// Composer mutations target one conversation, not arbitrary suffix matches.
	if tail, ok := strings.CutPrefix(path, "/v1/sessions/"); ok {
		id, action, hasAction := strings.Cut(tail, "/")
		if hasAction && id != "" && id != "." && id != ".." {
			switch action {
			case "mcp":
				return method == http.MethodGet || method == http.MethodPatch
			case "runtime/goal":
				return method == http.MethodPost
			}
		}
	}
	if method == http.MethodGet && shellPath(path) {
		return true
	}
	if method == http.MethodGet {
		switch path {
		case "/healthz", "/v1/capabilities", "/v1/providers", "/v1/models", "/v1/sidebar",
			"/v1/projects", "/v1/sessions", "/v1/sessions/status", "/v1/sessions/search",
			"/v1/attention", "/v1/events", "/v1/events/poll":
			return true
		}
		if hasAnyPrefix(path, "/dist/", "/images/", "/media/", "/files/", "/uploads/") {
			return true
		}
		if strings.HasPrefix(path, "/v1/responses/") {
			return true
		}
		if strings.HasPrefix(path, "/v1/sessions/") {
			return sessionReadAllowed(path)
		}
		return false
	}
	if method == http.MethodPost {
		switch path {
		case "/v1/sessions", "/v1/responses", "/v1/mentions/search", "/v1/linked-uploads":
			return true
		}
		if responsePostAllowed(path) {
			return true
		}
		if strings.HasPrefix(path, "/v1/sessions/") {
			return hasAnySuffix(path, "/approval", "/ask_user", "/attention/seen", "/code-cells/cancel")
		}
	}
	return false
}

func responsePostAllowed(path string) bool {
	tail, ok := strings.CutPrefix(path, "/v1/responses/")
	if !ok {
		return false
	}
	if strings.HasSuffix(path, "/cancel") {
		return true
	}
	id, action, _ := strings.Cut(tail, "/")
	return routeID(id) && toolResultAction(action, "client_tool_calls/")
}

// Project mode and single-workspace mode share the same worktree handlers.
func worktreeRouteAllowed(method, path string) bool {
	route, ok := strings.CutPrefix(path, "/v1/")
	if !ok {
		return false
	}
	if tail, project := strings.CutPrefix(route, "projects/"); project {
		id, suffix, found := strings.Cut(tail, "/")
		if !found || id == "" || id == "." || id == ".." {
			return false
		}
		route = suffix
	}
	switch route {
	case "worktrees":
		return method == http.MethodGet || method == http.MethodPost || method == http.MethodDelete
	case "worktrees/diff":
		return method == http.MethodGet
	case "worktrees/switch", "worktrees/merge", "worktrees/assisted-merge", "worktrees/promote":
		return method == http.MethodPost
	}
	return false
}

func shellPath(path string) bool {
	if path == "/" || path == "/manifest.webmanifest" || path == "/sw.js" || path == "/favicon.ico" {
		return true
	}
	if path == "/chat" {
		return true
	}
	if !strings.HasPrefix(path, "/chat/") {
		return false
	}
	rest := strings.TrimPrefix(path, "/chat/")
	return rest != "" && !strings.Contains(rest, "/") && !strings.Contains(rest, ".")
}

func sessionReadAllowed(path string) bool {
	rest := strings.TrimPrefix(path, "/v1/sessions/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 {
		return parts[0] != ""
	}
	suffix := parts[1]
	switch suffix {
	case "messages", "state", "transcript", "transcript/bodies", "attention/unseen", "children", "activity",
		"runtime/approvals", "code-cells", "diff-comments", "inspect", "inspect/bodies":
		return true
	}
	return strings.HasPrefix(suffix, "skill-runs/")
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func routeID(id string) bool {
	return id != "" && id != "." && id != ".."
}

func toolResultAction(action, prefix string) bool {
	tail, ok := strings.CutPrefix(action, prefix)
	if !ok {
		return false
	}
	id, rest, _ := strings.Cut(tail, "/")
	return routeID(id) && rest == "result"
}

// liveRouteAllowed excludes WebSocket audio (unsupported by the peer bridge).
// Session IDs and actions match exactly. The Live handler checks ownership.
func liveRouteAllowed(method, path string) bool {
	const root = "/v1/live/sessions"
	if path == root {
		return method == http.MethodPost
	}
	if !strings.HasPrefix(path, root+"/") {
		return false
	}
	id, action, _ := strings.Cut(strings.TrimPrefix(path, root+"/"), "/")
	if !strings.HasPrefix(id, "live_") || len(id) <= len("live_") || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	if path == root+"/"+id {
		return method == http.MethodDelete
	}
	switch action {
	case "events", "audio/output":
		return method == http.MethodGet
	case "audio/input", "diagnostics", "text", "session", "client_tools":
		return method == http.MethodPost
	default:
		return method == http.MethodPost && (toolResultAction(action, "tool_calls/") || toolResultAction(action, "delegations/"))
	}
}

// ToolResultID encodes a provider correlation ID as one peer-safe path segment.
// The provider's own ID is not changed; only the device-facing mailbox uses it.
func ToolResultID(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}
