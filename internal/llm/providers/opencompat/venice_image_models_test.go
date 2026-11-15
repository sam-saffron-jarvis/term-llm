package opencompat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/venice"
)

func TestVeniceImageModelDeferredCredentials(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("VENICE_API_KEY", "")
	marker := filepath.Join(t.TempDir(), "credential-executed")
	cfg := &config.Config{}
	cfg.Image.Venice.APIKey = "$(touch " + marker + "; exit 1)"
	cfg.Image.Venice.Model = "custom"
	for _, warm := range []bool{false, true} {
		if warm {
			if err := cache.WriteModelInfoCache(veniceImageCacheKey, []cache.CachedModel{{ID: "cached", Type: "image"}}); err != nil {
				t.Fatal(err)
			}
		}
		want := []string{"custom"}
		if warm {
			want = []string{"cached", "custom"}
		}
		if got := GetVeniceImageModelIDs(cfg, "image"); !reflect.DeepEqual(got, want) {
			t.Fatalf("models = %v, want %v", got, want)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("completion executed deferred credential: %v", err)
		}
	}
}

func TestVeniceImageCompletionCustomAndDeferredBaseURL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.WriteModelInfoCache(veniceImageCacheKey, []cache.CachedModel{{ID: "public", Type: "image"}}); err != nil {
		t.Fatal(err)
	}
	seen := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Path != "/proxy/models" || r.URL.RawQuery != "type=all" {
			t.Errorf("request URL = %s", r.URL)
		}
		io.WriteString(w, `{"data":[{"id":"private","type":"image"}]}`)
	}))
	defer server.Close()
	cfg := &config.Config{Providers: map[string]config.ProviderConfig{"venice": {BaseURL: "https://unused.example"}}}
	cfg.Image.Venice.APIKey = "test"
	cfg.Image.Venice.BaseURL = server.URL + "/proxy/"
	if got := GetVeniceImageModelIDs(cfg, "image"); len(got) != 0 {
		t.Fatalf("public cache leaked: %v", got)
	}
	if err := cache.WriteModelInfoCache(venice.MediaCacheKey(server.URL+"/proxy"), []cache.CachedModel{{ID: "private", Type: "image"}}); err != nil {
		t.Fatal(err)
	}
	if got := GetVeniceImageModelIDs(cfg, "image"); !reflect.DeepEqual(got, []string{"private"}) {
		t.Fatalf("models = %v", got)
	}
	if seen != 0 {
		t.Fatalf("requests = %d", seen)
	}
	cached, err := cache.ReadModelCache(veniceImageCacheKey)
	if err != nil || len(cached.ModelInfos) != 1 || cached.ModelInfos[0].ID != "public" {
		t.Fatalf("public cache = %+v, %v", cached, err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	cfg.Image.Venice.BaseURL = "$(touch " + marker + "; echo " + server.URL + ")"
	cfg.Image.Venice.Model = "configured"
	if got := GetVeniceImageModelIDs(cfg, "image"); !reflect.DeepEqual(got, []string{"configured"}) {
		t.Fatalf("deferred models = %v", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("completion executed deferred URL: %v", err)
	}
	if seen != 0 {
		t.Fatalf("unexpected request for deferred endpoint")
	}
}
