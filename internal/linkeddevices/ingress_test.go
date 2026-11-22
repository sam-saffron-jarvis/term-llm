package linkeddevices

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareMountedRequestStripsExactlyOnce(t *testing.T) {
	tests := []struct {
		name, basePath, target, wantPath string
		wantErr                          bool
	}{
		{name: "root", basePath: "/", target: "/v1/sessions?limit=1", wantPath: "/v1/sessions"},
		{name: "mount root", basePath: "/chat", target: "/chat", wantPath: "/"},
		{name: "mounted API", basePath: "/chat", target: "/chat/v1/sessions?limit=1", wantPath: "/v1/sessions"},
		{name: "deep mount", basePath: "/deep/mount", target: "/deep/mount/dist/app.js", wantPath: "/dist/app.js"},
		{name: "strip once", basePath: "/chat", target: "/chat/chat/v1/sessions", wantPath: "/chat/v1/sessions"},
		{name: "neighbor", basePath: "/chat", target: "/chatty/v1/sessions", wantErr: true},
		{name: "unmounted", basePath: "/chat", target: "/v1/sessions", wantErr: true},
		{name: "encoded mount", basePath: "/chat", target: "/ch%61t/v1/sessions", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			err := prepareMountedRequest(request, test.basePath)
			if (err != nil) != test.wantErr {
				t.Fatalf("prepareMountedRequest() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && request.URL.Path != test.wantPath {
				t.Fatalf("path = %q, want %q", request.URL.Path, test.wantPath)
			}
		})
	}
}

func TestRouteAllowedDefaultDeny(t *testing.T) {
	tests := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/", true},
		{http.MethodGet, "/chat/session-1", true},
		{http.MethodGet, "/chat/session-1/extra", false},
		{http.MethodGet, "/future-owner-route", false},
		{http.MethodGet, "/dist/app.js", true},
		{http.MethodGet, "/v1/sessions", true},
		{http.MethodPost, "/v1/sessions", true},
		{http.MethodGet, "/v1/sessions/s1/messages", true},
		{http.MethodGet, "/v1/sessions/s1/children", true},
		{http.MethodGet, "/v1/sessions/s1/activity", true},
		{http.MethodGet, "/v1/sessions/s1/activity/runs/private", false},
		{http.MethodGet, "/v2/runs/private", false},
		{http.MethodGet, "/v2/jobs", false},
		{http.MethodGet, "/v1/sessions/s1/transcript", true},
		{http.MethodGet, "/v1/sessions/s1/file-changes/content", false},
		{http.MethodGet, "/v1/sessions/s1/shares", false},
		{http.MethodGet, "/v1/sessions/s1/tree", false},
		{http.MethodPost, "/v1/sessions/s1/approval", true},
		{http.MethodPost, "/v1/sessions/s1/ask_user", true},
		{http.MethodPost, "/v1/responses", true},
		{http.MethodGet, "/v1/responses/r1/events", true},
		{http.MethodPost, "/v1/responses/r1/cancel", true},
		{http.MethodPost, "/v1/responses/r1/client_tool_calls/call_1/result", true},
		{http.MethodPatch, "/v1/responses/r1/client_tool_calls/call_1/result", false},
		{http.MethodPost, "/v1/responses/r1/client_tool_calls/call_1/result/", false},
		{http.MethodPost, "/v1/responses/r1/client_tool_calls//result", false},
		{http.MethodPost, "/v1/responses//client_tool_calls/call_1/result", false},
		{http.MethodPost, "/v1/responses/r1/extra/client_tool_calls/call_1/result", false},
		// Idle script Stop intentionally has the same paired-device authority as response Stop.
		{http.MethodGet, "/v1/sessions/s1/code-cells", true},
		{http.MethodPost, "/v1/sessions/s1/code-cells/cancel", true},
		{http.MethodPost, "/v1/sessions/s1/code-cells", false},
		{http.MethodDelete, "/v1/sessions/s1/code-cells", false},
		{http.MethodGet, "/files/result.png", true},
		{http.MethodDelete, "/v1/sessions/s1", false},
		{http.MethodPatch, "/v1/sessions/s1", false},
		{http.MethodGet, "/v1/sessions/s1/shell/stream", false},
		{http.MethodGet, "/v1/sessions/s1/mcp", true},
		{http.MethodPatch, "/v1/sessions/s1/mcp", true},
		{http.MethodPost, "/v1/sessions/s1/mcp", false},
		{http.MethodPatch, "/v1/sessions/s1/nested/mcp", false},
		{http.MethodPost, "/v1/sessions/s1/runtime/goal", true},
		{http.MethodPost, "/v1/sessions/s1/extra/runtime/goal", false},
		{http.MethodPost, "/v1/linked-uploads", true},
		{http.MethodPost, "/v1/linked-uploads/extra", false},
		{http.MethodGet, "/v1/linked-uploads", false},
		{http.MethodPost, "/uploads", false},
		{http.MethodGet, "/admin/widgets/status", false},
		{http.MethodGet, "/v1/linked-devices", false},
		{http.MethodPost, "/v1/linked-devices/stop", false},
		{http.MethodGet, "/v1/mcp/catalogue", false},
		{http.MethodGet, "/v2/jobs", false},
		{http.MethodGet, "/api/sessions/s1", false},
		{http.MethodGet, "/auth/security", false},
		{http.MethodGet, "/extensions/example/main.js", false},
		{http.MethodGet, "/unknown.json", false},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			if got := RouteAllowed(test.method, test.path); got != test.want {
				t.Fatalf("RouteAllowed(%q, %q) = %v, want %v", test.method, test.path, got, test.want)
			}
		})
	}
}

func TestIdentityRequestTrusted(t *testing.T) {
	tests := []struct {
		name                         string
		requireAuth                  bool
		token, presented             string
		passkeyMode, passkey, device bool
		want                         bool
	}{
		{name: "auth disabled", want: true},
		{name: "missing authority", requireAuth: true, token: "secret"},
		{name: "bearer", requireAuth: true, token: "secret", presented: "secret", want: true},
		{name: "wrong bearer", requireAuth: true, token: "secret", presented: "wrong"},
		{name: "passkey mode rejects bearer", requireAuth: true, token: "secret", presented: "secret", passkeyMode: true},
		{name: "passkey", requireAuth: true, token: "secret", passkeyMode: true, passkey: true, want: true},
		{name: "device in passkey mode", requireAuth: true, token: "secret", passkeyMode: true, device: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if test.presented != "" {
				request.Header.Set("Authorization", "Bearer "+test.presented)
			}
			if test.device {
				request = WithDevicePrincipal(request, DevicePrincipal{DeviceID: "device", Scope: ScopeWebV1})
			}
			if got := IdentityRequestTrusted(request, test.requireAuth, test.token, test.passkeyMode, test.passkey); got != test.want {
				t.Fatalf("IdentityRequestTrusted() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAdmitDevice(t *testing.T) {
	ownerCalls := 0
	owner := func(w http.ResponseWriter, _ *http.Request) { ownerCalls++; w.WriteHeader(http.StatusUnauthorized) }
	next := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	handler := AdmitDevice(next, owner)

	request := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusUnauthorized || ownerCalls != 1 {
		t.Fatalf("ordinary request = %d, owner calls %d", recorder.Code, ownerCalls)
	}

	request = WithDevicePrincipal(request, DevicePrincipal{DeviceID: "dev_1", Scope: ScopeWebV1})
	recorder = httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusNoContent || ownerCalls != 1 {
		t.Fatalf("device request = %d, owner calls %d", recorder.Code, ownerCalls)
	}
}

func TestWorktreeRouteScope(t *testing.T) {
	for _, base := range []string{"/v1/worktrees", "/v1/projects/prj_a/worktrees"} {
		for action, methods := range map[string]string{"": "GET POST DELETE", "/switch": "POST", "/diff": "GET", "/merge": "POST", "/assisted-merge": "POST", "/promote": "POST", "/content": "", "/merge/other": ""} {
			for _, method := range []string{"GET", "POST", "PATCH", "DELETE"} {
				if got := RouteAllowed(method, base+action); got != strings.Contains(methods, method) {
					t.Errorf("%s %s: %t", method, base+action, got)
				}
			}
		}
	}
	for _, path := range []string{"/v1/projects/../worktrees", "/v1/projects//worktrees", "/v1/projects/a/nested/worktrees", "/v1/projects/a"} {
		if RouteAllowed("DELETE", path) {
			t.Errorf("unexpected admission: %s", path)
		}
	}
}
