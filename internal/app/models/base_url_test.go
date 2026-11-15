package models

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestNativeModelListerBaseURL(t *testing.T) {
	for _, kind := range []config.ProviderType{config.ProviderTypeOpenRouter, config.ProviderTypeXAI, config.ProviderTypeVenice, config.ProviderTypeSambaNova, config.ProviderTypeNearAI, config.ProviderTypeZen} {
		t.Run(string(kind), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				want := "/proxy/v1/models"
				if kind == config.ProviderTypeNearAI {
					want = "/proxy/v1/model/list"
				}
				if r.URL.Path != want {
					t.Errorf("path = %q", r.URL.Path)
				}
				fmt.Fprint(w, `{"data":[{"id":"example"}]}`)
			}))
			defer server.Close()
			cfg := config.ProviderConfig{Type: kind, BaseURL: server.URL + "/proxy/v1//", ResolvedAPIKey: "test"}
			lister, err := newModelLister(string(kind), kind, cfg, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lister.ListModels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if requests == 0 {
				t.Fatal("no request reached configured endpoint")
			}
		})
	}
}
