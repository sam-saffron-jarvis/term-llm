package linkeddevices_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/linkeddevices/testdevice"
	"github.com/term-llm/term-llm/internal/peerbroker"
)

func TestMCPManagementOnConnectedDevice(t *testing.T) {
	b, err := peerbroker.NewHandler(peerbroker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	broker := httptest.NewServer(b)
	defer broker.Close()
	lifetimes := make(chan context.Context, 1)
	opts := linkeddevices.Options{DataDir: t.TempDir(), Target: "manage", StoreInstanceID: "store", Authenticated: true, BrokerURL: broker.URL, BasePath: "/mount", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := linkeddevices.DeviceFromRequest(r)
		if !ok {
			t.Error("missing device principal")
		}
		if r.URL.Path == "/v1/capabilities" {
			lifetimes <- p.MCPGrantLifetime
			payload := map[string]any{}
			linkeddevices.ShapeCapabilities(w, r, payload)
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		if !p.ManageMCP {
			t.Error("management dispatched without permission")
		}
		w.WriteHeader(204)
	})}
	runtime, err := linkeddevices.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sas := make(chan string, 1)
	device, err := testdevice.New(testdevice.Config{StatePath: t.TempDir() + "/device.json", OnSAS: func(code string) { sas <- code }})
	if err != nil {
		t.Fatal(err)
	}
	pairRuntimeDevice(t, ctx, runtime, device, sas)
	session, err := device.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	id := runtime.Status().Devices[0].ID
	ownerPath := "/v1/linked-devices/devices/" + id + "/mcp-management"
	owner := func(body string, peer bool, want int) {
		t.Helper()
		req := httptest.NewRequest("POST", ownerPath, strings.NewReader(body))
		if peer {
			req = linkeddevices.WithDevicePrincipal(req, linkeddevices.DevicePrincipal{DeviceID: id, Scope: linkeddevices.ScopeWebV1, ManageMCP: true})
		}
		rr := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(rr, req)
		if rr.Code != want {
			t.Fatalf("owner API: %d, want %d", rr.Code, want)
		}
	}
	peer := func(method, path string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://peer/mount"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Manage-MCP", "true")
		res, err := session.HTTPClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d", method, path, res.StatusCode, want)
		}
	}
	check := func(enabled bool) context.Context {
		t.Helper()
		res, err := session.HTTPClient().Get("http://peer/mount/v1/capabilities")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var caps struct {
			Linked map[string]any `json:"linked_devices"`
		}
		if err := json.NewDecoder(res.Body).Decode(&caps); err != nil {
			t.Fatal(err)
		}
		if caps.Linked["mcp_manage"] != enabled || caps.Linked["owner_controls"] != false {
			t.Fatalf("unexpected capabilities: %+v", caps)
		}
		want := 403
		if enabled {
			want = 204
		}
		peer("GET", "/v1/mcp/catalogue?q=time", want)
		peer("POST", "/v1/mcp/servers", want)
		peer("DELETE", "/v1/mcp/servers/sample.time", want)
		peer("POST", "/v1/sessions/abc/mcp/web.exa/oauth/start", want)
		peer("POST", "/v1/sessions/abc/mcp/exa/oauth/cancel", want)
		peer("GET", "/v1/mcp/oauth/flows/"+strings.Repeat("a", 32), want)
		lifetime := <-lifetimes
		if enabled != (lifetime != nil) || lifetime != nil && lifetime.Err() != nil {
			t.Fatalf("unexpected MCP grant lifetime for enabled=%v", enabled)
		}
		return lifetime
	}
	owner(`{"enabled":true}`, true, 403)
	owner(`{}`, false, 400)
	check(false)
	owner(`{"enabled":true}`, false, 204)
	first := check(true)
	peer("POST", ownerPath, 403)
	peer("GET", "/v1/mcp/oauth/callback", 403)
	peer("DELETE", "/v1/sessions/abc/mcp/exa/oauth", 403)
	peer("POST", "/v1/sessions/abc/mcp/exa/oauth/logout", 403)
	peer("POST", "/v1/sessions/abc/mcp/exa/oauth/%73tart", 403)
	peer("PUT", "/v1/mcp/servers/time", 403)
	peer("DELETE", "/v1/mcp/servers/time/tools", 403)
	peer("GET", "/v1/mcp/%63atalogue", 403)
	owner(`{"enabled":false}`, false, 204)
	if first.Err() == nil {
		t.Fatal("disabled grant left its flow lifetime active")
	}
	check(false)
	owner(`{"enabled":true}`, false, 204)
	second := check(true)
	if first.Err() == nil {
		t.Fatal("re-enabling resurrected a prior lifetime")
	}
	session.Close()
	if second.Err() != nil {
		t.Fatal("peer disconnect revoked its grant")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if second.Err() == nil {
		t.Fatal("host shutdown left flow lifetime active")
	}
	reopened, err := linkeddevices.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if ds := reopened.Status().Devices; len(ds) != 1 || ds[0].ID != id || !ds[0].ManageMCP {
		t.Fatalf("permission lost on restart: %+v", ds)
	}
	if err := reopened.Start(); err != nil {
		t.Fatal(err)
	}
	afterRestart, err := device.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer afterRestart.Close()
	response, err := afterRestart.HTTPClient().Get("http://peer/mount/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	third := <-lifetimes
	if third == nil || third.Err() != nil {
		t.Fatal("missing grant after restart")
	}
	if err := reopened.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if third.Err() == nil {
		t.Fatal("device revoke left pending flow lifetime active")
	}

}
