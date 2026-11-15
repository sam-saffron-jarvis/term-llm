package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestConfiguredProviderRequests(t *testing.T) {
	var path string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; http.Error(w, "test", 400) }))
	defer s.Close()
	cfg := &config.Config{}
	cfg.Image.Venice.BaseURL = s.URL + "/proxy/"
	p, err := NewConfiguredVeniceProvider(cfg, "key")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Quote(context.Background(), Request{})
	if path != "/proxy/video/quote" {
		t.Fatalf("path %q", path)
	}
}
