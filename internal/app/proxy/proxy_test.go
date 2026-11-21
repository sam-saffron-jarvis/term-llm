package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/term-llm/term-llm/internal/providerspec"
	"github.com/term-llm/term-llm/internal/proxyprofile"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testService(t testing.TB) *Service {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	admin := filepath.Join(dir, "admin")
	if err := os.WriteFile(admin, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(Options{Auth: "token", StateDir: state, AdminTokenFile: admin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func boundMaterial(t testing.TB, _ string, value string) material {
	t.Helper()
	return material{Value: value}
}

// fixtureProviders fills the persisted model for unrelated transport tests. Tests
// of structural rejection call the real compiler without this fixture helper.
func fixtureProviders(c Config) Config {
	c.Routes = slices.Clone(c.Routes)
	provs := make(map[string]Provider)
	maps.Copy(provs, c.Providers)
	for i := range c.Routes {
		r := &c.Routes[i]
		if r.Provider == "" {
			r.Provider = r.Name
		}
		if r.Profile == "" {
			if _, builtin := providerspec.Lookup(r.Name); builtin {
				if p, ok := proxyprofile.ByID(r.Name); ok {
					r.Profile = p.ID
				}
			}
		}
		if _, ok := provs[r.Provider]; !ok {
			provs[r.Provider] = Provider{Label: r.Provider}
		}
	}
	c.Providers = provs
	return c
}

func canonicalTestProfile(id string) string { return proxyprofile.CanonicalParent(id) }

func configure(t testing.TB, s *Service, c Config, secrets map[string]material) {
	t.Helper()
	snap, err := compile(fixtureProviders(c), s.snap.revision+1, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.saveConfig(snap, false); err != nil {
		t.Fatal(err)
	}
	s.snap = snap
}
func route(name, prefix, upstream, header, secret string) Route {
	return Route{Name: name, LocalPrefix: prefix, Upstream: upstream, Enabled: true, AllowHTTP: true, CallerAuth: Auth{Header: header, Prefix: "Local "}, SetHeaders: map[string]HeaderRule{"X-Upstream-Key": {Secret: secret, Prefix: "Real "}}}
}
func caller(t testing.TB, s *Service, routes ...string) string {
	t.Helper()
	_, token, err := s.store.createCaller("test caller", routes, s.snap)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func call(s *Service, method, target, header, token string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set(header, "Local "+token)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}
func TestTwoUnrelatedUpstreamsOpaqueSemantics(t *testing.T) {
	var a, b atomic.Int32
	upstream := func(count *atomic.Int32, base, auth, want string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			if r.URL.EscapedPath() != base+"opaque%2Fid" || r.URL.RawQuery != "q=a%2Fb&invalid=%zz&x=1;x=2" {
				t.Errorf("wire target %s query %s", r.URL.EscapedPath(), r.URL.RawQuery)
			}
			if r.Header.Get(auth) != want || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Caller-Key") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Term-LLM-Proxy-Private") != "" || r.Header.Get("X-Remove-Me") != "" {
				t.Errorf("credential or private header leaked: %v", r.Header)
			}
			if r.Host != strings.TrimPrefix(r.URL.Scheme+"://"+r.Host, r.URL.Scheme+"://") {
				t.Error("host")
			}
			body, _ := io.ReadAll(r.Body)
			if !bytes.Equal(body, []byte{0, 1, 255, 42}) {
				t.Errorf("body changed %v", body)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Set-Cookie", "upstream=secret")
			w.Header().Set("X-Native", "kept")
			w.WriteHeader(422)
			w.Write(body)
		}))
	}
	one := upstream(&a, "/alpha/", "X-Upstream-Key", "Real one")
	defer one.Close()
	two := upstream(&b, "/different/base/", "X-Other-Auth", "Token two")
	defer two.Close()
	s := testService(t)
	r1 := route("one", "/local-a/", one.URL+"/alpha/", "X-Caller-Key", "one")
	r2 := route("two", "/local-b/", two.URL+"/different/base/", "X-Caller-Key", "two")
	r2.SetHeaders = map[string]HeaderRule{"X-Other-Auth": {Secret: "two", Prefix: "Token "}}
	configure(t, s, Config{Routes: []Route{r1, r2}}, map[string]material{"one": {Value: "one"}, "two": {Value: "two"}})
	token := caller(t, s, "one", "two")
	for _, prefix := range []string{"/local-a/", "/local-b/"} {
		req := httptest.NewRequest("POST", prefix+"opaque%2Fid?q=a%2Fb&invalid=%zz&x=1;x=2", bytes.NewReader([]byte{0, 1, 255, 42}))
		req.Header.Set("X-Caller-Key", "Local "+token)
		req.Header.Set("Authorization", "untrusted")
		req.Header.Set("Cookie", "local=private")
		req.Header.Set("X-Forwarded-For", "spoofed")
		req.Header.Set("X-Term-LLM-Proxy-Private", "private")
		req.Header.Set("Connection", "X-Remove-Me")
		req.Header.Set("X-Remove-Me", "gone")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 422 || !bytes.Equal(rr.Body.Bytes(), []byte{0, 1, 255, 42}) || rr.Header().Get("Set-Cookie") != "" || rr.Header().Get("X-Native") != "kept" {
			t.Fatalf("response changed %d %v %v", rr.Code, rr.Header(), rr.Body.Bytes())
		}
	}
	if a.Load() != 1 || b.Load() != 1 {
		t.Fatal("unexpected dispatch counts")
	}
	records, _, err := s.store.records(nil, 50)
	if err != nil || len(records) != 2 {
		t.Fatalf("ledger %v %v", records, err)
	}
	for _, r := range records {
		if r.Status != 422 || r.Outcome != "http_error" || r.Sent != 4 || r.Received != 4 || strings.Contains(r.Path, "?") {
			t.Errorf("record %+v", r)
		}
	}
}
func TestStartFailureNeverDispatches(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "secret")}}, map[string]material{"secret": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	if _, err := s.store.db.Exec("DROP TABLE requests"); err != nil {
		t.Fatal(err)
	}
	rr := call(s, "POST", "/r/path", "X-Local", token, strings.NewReader("billable"))
	if rr.Code != 503 || n.Load() != 0 {
		t.Fatalf("code %d dispatch %d", rr.Code, n.Load())
	}
}
func TestRouteValidation(t *testing.T) {
	base := route("valid", "/valid/", "https://example.test/base/", "Authorization", "key")
	tests := []struct {
		name   string
		change func(*Route)
	}{
		{"root", func(r *Route) { r.LocalPrefix = "/" }}, {"reserved", func(r *Route) { r.LocalPrefix = "/_proxy/anything/" }},
		{"traversal", func(r *Route) { r.Upstream = "https://example.test/a/../" }}, {"userinfo", func(r *Route) { r.Upstream = "https://u:p@example.test/" }},
		{"query", func(r *Route) { r.Upstream = "https://example.test/?a=b" }}, {"insecure", func(r *Route) { r.Upstream = "http://example.test/"; r.AllowHTTP = false }},
		{"header", func(r *Route) { r.SetHeaders = map[string]HeaderRule{"Host": {Secret: "key"}} }}, {"duplicate", func(r *Route) {
			r.SetHeaders = map[string]HeaderRule{"X-Key": {Secret: "key"}, "x-key": {Secret: "key"}}
		}},
		{"missing", func(r *Route) { r.SetHeaders = map[string]HeaderRule{"X-Key": {Secret: "missing"}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base
			tt.change(&r)
			if _, err := compile(fixtureProviders(Config{Routes: []Route{r}}), 1, map[string]material{"key": {Value: "secret"}}); err == nil {
				t.Fatal("accepted invalid route")
			}
		})
	}
	for _, p := range []string{"/r/../x", "/r/%2e%2e/x", "/r/%252e%252e/x", "/r/%5cfoo", "/r/a%00"} {
		if safePath(p) {
			t.Errorf("accepted unsafe %s", p)
		}
	}
	if !safePath("/r/id%2Fpart") {
		t.Fatal("escaped identifier rejected")
	}
}
func TestAuthGrantsAndOriginChanges(t *testing.T) {
	s := testService(t)
	r := route("r", "/r/", "https://example.test/", "X-Local", "key")
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "real")})
	token := caller(t, s, "r")
	if _, err := s.store.authorize(token, s.snap.routes[0]); err != nil {
		t.Fatal(err)
	}
	r.Upstream = "https://other.test/"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "real")})
	if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
		t.Fatal("destination change retained grant")
	}
	r.Upstream = "https://example.test/"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "real")})
	if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
		t.Fatal("destination revert restored grant")
	}
	token = caller(t, s, "r")
	rr := call(s, "GET", "/r/x", "X-Local", strings.Repeat("a", 64), nil)
	if rr.Code != 401 {
		t.Fatal("admin credential authorized forwarding")
	}
	req := httptest.NewRequest("GET", "/r/x", nil)
	req.Header["X-Local"] = []string{"Local " + token, "Local " + token}
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatal("duplicate auth authorized")
	}
	configure(t, s, Config{}, nil)
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "real")})
	if _, err := s.store.authorize(token, s.snap.routes[0]); err == nil {
		t.Fatal("recreated route inherited grants")
	}
}
func adminRequest(s *Service, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	if path == "config" {
		if fields, ok := body.(map[string]any); ok {
			if routes, ok := fields["routes"].([]Route); ok && fields["providers"] == nil {
				c := fixtureProviders(Config{Routes: routes})
				fields = maps.Clone(fields)
				fields["routes"], fields["providers"] = c.Routes, c.Providers
				body = fields
			}
		}
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://localhost/_proxy/api/"+path, bytes.NewReader(data))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}
func loginAdmin(t *testing.T, s *Service) (*http.Cookie, string) {
	t.Helper()
	rr := adminRequest(s, "POST", "login", map[string]string{"token": strings.Repeat("a", 64)}, nil, "")
	if rr.Code != 200 {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	var v map[string]string
	json.Unmarshal(rr.Body.Bytes(), &v)
	return rr.Result().Cookies()[0], v["csrf"]
}
func TestAdminSecurityAndWriteOnlySecrets(t *testing.T) {
	s := testService(t)
	for _, host := range []string{"evil.test", "localhost.evil.test", "192.168.1.10"} {
		req := httptest.NewRequest("GET", "http://"+host+"/_proxy/", nil)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 403 {
			t.Errorf("Host %s accepted", host)
		}
	}
	cookie, csrf := loginAdmin(t, s)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("cookie security")
	}
	rr := adminRequest(s, "PUT", "secrets", map[string]string{"name": "key", "value": "server-held-sensitive"}, cookie, "")
	if rr.Code != 403 {
		t.Fatal("CSRF not required")
	}
	rr = adminRequest(s, "PUT", "secrets", map[string]string{"name": "key", "value": "server-held-sensitive"}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	rr = adminRequest(s, "GET", "state", nil, cookie, csrf)
	if strings.Contains(rr.Body.String(), "server-held-sensitive") {
		t.Fatal("secret disclosed")
	}
	rev := s.snap.revision
	r := route("r", "/r/", "https://example.test/", "X-Local", "key")
	rr = adminRequest(s, "PUT", "config", map[string]any{"revision": rev, "routes": []Route{r}}, cookie, csrf)
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	rr = adminRequest(s, "DELETE", "secrets", map[string]string{"name": "key"}, cookie, csrf)
	if rr.Code != 409 {
		t.Fatal("deleted referenced secret")
	}
	rr = adminRequest(s, "PUT", "secrets", map[string]any{"name": "key", "origin": "https://attacker.test/", "value": "new", "replace": true}, cookie, csrf)
	if rr.Code != 400 {
		t.Fatal("accepted removed origin field")
	}
	rr = adminRequest(s, "PUT", "config", map[string]any{"revision": rev, "routes": []Route{r}}, cookie, csrf)
	if rr.Code != 409 {
		t.Fatal("stale update accepted")
	}
	rr = adminRequest(s, "POST", "import", map[string]any{"revision": s.snap.revision, "yaml": "routes: []\nsecrets:\n  bad:\n    file: /etc/passwd\n"}, cookie, csrf)
	if rr.Code != 400 {
		t.Fatalf("file ref import %d", rr.Code)
	}
	req := httptest.NewRequest("POST", "http://localhost/_proxy/api/logout", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://evil.test")
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatal("cross origin accepted")
	}
}
func TestUnknownPaginationRetentionAndPersistence(t *testing.T) {
	s := testService(t)
	for i := 0; i < 4; i++ {
		r := Record{ID: fmt.Sprintf("id%d", i), Started: time.Now().UnixMilli(), Route: "route", CallerID: "caller", Outcome: "unknown", Path: "safe"}
		if err := s.store.start(context.Background(), &r); err != nil {
			t.Fatal(err)
		}
	}
	records, next, err := s.store.records(nil, 2)
	if err != nil || len(records) != 2 || next != records[1].Seq {
		t.Fatalf("pagination %v %d %v", records, next, err)
	}
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "GET", "requests", nil, cookie, csrf)
	if !strings.Contains(rr.Body.String(), `"outcome":"unknown"`) {
		t.Fatal(rr.Body.String())
	}
	if _, err := s.store.db.Exec("UPDATE requests SET started=0 WHERE id='id0'"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.retain(context.Background(), 30); err != nil {
		t.Fatal(err)
	}
	_, rev, _, err := s.store.load()
	if err != nil || rev == 0 {
		t.Fatal("state load failed")
	}
}
func TestFileModeAdminAliasAndReload(t *testing.T) {
	s := testService(t)
	ref := Config{Secrets: map[string]SecretRef{"bad": {File: s.opts.AdminTokenFile}}}
	if _, err := resolveSecrets(ref, nil, s.opts.AdminTokenFile, true); err == nil {
		t.Fatal("admin file readable as upstream secret")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Link(s.opts.AdminTokenFile, alias); err == nil {
		ref.Secrets["bad"] = SecretRef{File: alias}
		if _, err := resolveSecrets(ref, nil, s.opts.AdminTokenFile, true); err == nil {
			t.Fatal("admin hardlink readable")
		}
	}
	file := filepath.Join(t.TempDir(), "proxy.yaml")
	os.WriteFile(file, []byte("routes: []\n"), 0600)
	s.opts.ConfigFile = file
	cookie, csrf := loginAdmin(t, s)
	rr := adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": []Route{}}, cookie, csrf)
	if rr.Code != 409 {
		t.Fatal("file mode editable")
	}
	old := s.snap
	os.WriteFile(file, []byte("unknown: bad\n"), 0600)
	rr = adminRequest(s, "POST", "reload", map[string]any{}, cookie, csrf)
	if rr.Code != 400 || s.snap != old {
		t.Fatal("bad reload replaced snapshot")
	}
}
