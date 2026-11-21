package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/app/uploads"
	"github.com/term-llm/term-llm/internal/linkeddevices"
)

func TestLinkedUploadRequiresConnectedDevice(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s := uploadTestServer()
	handler := s.httpHandler()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closed, closePeer := context.WithCancel(t.Context())
	closePeer()
	for _, tc := range []struct {
		name   string
		p      *linkeddevices.DevicePrincipal
		status int
	}{
		{"bearer only", nil, 403},
		{"no lifetime", &linkeddevices.DevicePrincipal{DeviceID: "phone", Scope: linkeddevices.ScopeWebV1}, 403},
		{"disconnected", &linkeddevices.DevicePrincipal{DeviceID: "phone", Scope: linkeddevices.ScopeWebV1, Lifetime: closed}, 403},
		{"connected", &linkeddevices.DevicePrincipal{DeviceID: "phone", Scope: linkeddevices.ScopeWebV1, Lifetime: ctx}, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/chat/v1/linked-uploads", nil)
			r.Header.Set(uploads.FilenameHeader, "empty.bin")
			r.Header.Set("X-Term-LLM-Upload-Offset", "0")
			r.Header.Set("X-Term-LLM-Upload-Size", "0")
			if tc.p != nil {
				r = linkeddevices.WithDevicePrincipal(r, *tc.p)
			} else {
				r.Header.Set("Authorization", "Bearer "+s.cfg.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != tc.status {
				t.Fatalf("got %d: %s", rec.Code, rec.Body)
			}
		})
	}
}
