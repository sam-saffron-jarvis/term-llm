package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrustedAccountInjectsIdentityEvenWhenGatewayClientHasNone(t *testing.T) {
	for _, kind := range []string{"chatgpt", "grok"} {
		t.Run(kind, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers := []string{"ChatGPT-Account-ID"}
				if kind == "grok" {
					headers = []string{"X-Userid", "X-Grok-User-ID"}
				}
				for _, h := range headers {
					if r.Header.Get(h) != "synthetic-account-id" {
						t.Errorf("missing trusted %s: %q", h, r.Header.Get(h))
					}
				}
				io.WriteString(w, "ok")
			}))
			defer up.Close()
			s := testService(t)
			testAccount(t, s, kind, up, false)
			token := configureAccount(t, s, accountRoute(up.URL))
			req := httptest.NewRequest("POST", "/account/responses", strings.NewReader(`{"model":"fixture"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			if rr.Code != 200 {
				t.Fatal(rr.Code, rr.Body.String())
			}
		})
	}
}
