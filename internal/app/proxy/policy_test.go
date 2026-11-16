package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPolicyLegacyAuthorityGoldens(t *testing.T) {
	// Independent pre-extension vectors from workspace LEGACY-AUTHORITY-GOLDENS.json.
	r := Route{Upstream: "https://api.example.test/base/"}
	u, _ := url.Parse(r.Upstream)
	origin, _ := Origin(r.Upstream)
	for _, tc := range []struct {
		rules map[string]HeaderRule
		want  string
	}{
		{nil, "6faec26371332d536ab61b227b8f9054aae640dd95dc0520755d75fc5a0fe843"},
		{map[string]HeaderRule{"Authorization": {Secret: "key", Prefix: "Bearer "}}, "4bc833f091bbb88057ca774fa6b31c3cc3962238203e907fd0e686aa6863988a"},
	} {
		r.SetHeaders = tc.rules
		if got := routeAuthority(r, origin, u); got != tc.want {
			t.Fatalf("legacy fingerprint %s want %s", got, tc.want)
		}
	}
	base := routeAuthority(r, origin, u)
	for _, mutate := range []func(*Route){func(r *Route) { r.Account = "account" }, func(r *Route) { r.WebSocket = true }, func(r *Route) { r.Allow = []string{"POST /responses"} }} {
		next := r
		mutate(&next)
		if routeAuthority(next, origin, u) == base {
			t.Fatal("scope change did not invalidate authority")
		}
	}
	r.Usage, r.Redirects = "auto", "refuse"
	if routeAuthority(r, origin, u) != base {
		t.Fatal("observation/redirect policy changed authority")
	}
}

func TestPolicyValidationAndMatching(t *testing.T) {
	for _, rule := range []string{"post /v1", "GET /a/../b", "GET /a//b", "GET /a%2fb", "GET /a?b", "GET  /a", "CONNECT /", "GET /a\\b", "GET /a#b", "GET relative"} {
		r := Route{Allow: []string{rule}}
		if validatePolicy(&r) == nil {
			t.Errorf("accepted %q", rule)
		}
	}
	for _, r := range []Route{{Account: "../account"}, {Redirects: "follow"}, {Usage: "on"}, {Allow: []string{"GET /", "GET /"}}} {
		if validatePolicy(&r) == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	r := Route{Allow: []string{"POST /responses", "GET /models/"}}
	if err := validatePolicy(&r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/responses", true}, {"POST", "/responses/next", true}, {"GET", "/models/id", true},
		{"GET", "/responses", false}, {"POST", "/%72esponses", false}, {"POST", "/responses%252fadmin", false},
		{"POST", "/responses/../admin", false}, {"POST", "/responses//admin", false},
	} {
		if got := policyAllows(r, tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %v", tc.method, tc.path, got)
		}
	}
	a := r
	a.Allow = []string{"GET /models/", "POST /responses"}
	_ = validatePolicy(&a)
	if strings.Join(r.Allow, ",") != strings.Join(a.Allow, ",") {
		t.Fatal("rule order not canonical")
	}
}

func TestPolicyDeniedLedgerAndRedirectRefusal(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://private.invalid/secret")
		w.WriteHeader(307)
	}))
	defer up.Close()
	s := testService(t)
	r := route("test", "/local/", up.URL, "X-Local", "key")
	r.Allow = []string{"GET /allowed"}
	r.Redirects = "refuse"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "key")})
	token := caller(t, s, "test")
	for _, p := range []string{"denied", "%61llowed", "allowed%2fadmin", "allowed%252fadmin"} {
		rr := call(s, "GET", "/local/"+p, "X-Local", token, nil)
		if rr.Code != 403 || rr.Header().Get("X-Term-LLM-Proxy-Request-ID") == "" {
			t.Fatalf("denial %s: %d", p, rr.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("denied request dispatched")
	}
	rr := call(s, "GET", "/local/allowed", "X-Local", token, nil)
	if rr.Code != 502 || rr.Header().Get("Location") != "" || strings.Contains(rr.Body.String(), "private.invalid") || rr.Header().Get("X-Term-LLM-Proxy-Redirect") != "refused" {
		t.Fatalf("redirect response: %d %v %s", rr.Code, rr.Header(), rr.Body.String())
	}
	records, _, err := s.store.records(nil, 50)
	if err != nil || len(records) != 5 {
		t.Fatalf("ledger %v %v", records, err)
	}
	denied := 0
	for _, rec := range records {
		if rec.Outcome == "denied" {
			denied++
		}
	}
	if denied != 4 {
		t.Fatalf("denied records = %d", denied)
	}
	r.Redirects = ""
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "key")})
	rr = call(s, "GET", "/local/allowed", "X-Local", token, nil)
	if rr.Code != 307 || rr.Header().Get("Location") == "" {
		t.Fatal("default redirects changed")
	}
}
