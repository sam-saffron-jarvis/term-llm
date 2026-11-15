package venice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
)

// Catalog values are synthetic, not provider compatibility certification.
func TestTypedCatalogAndRootScope(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay/v1/models" || r.URL.RawQuery != "type=video" || r.Header.Get("Authorization") != "Bearer test-only" {
			t.Errorf("request %s headers %v", r.URL, r.Header)
		}
		fmt.Fprint(w, `{"data":[{"id":"z","type":"video","model_spec":{"name":"Synthetic Z","privacy":"private","constraints":{"model_type":"text-to-video","durations":["4s","6s","8s"],"resolutions":["360p","4k"]}}},{"id":"a","type":"video"},{"id":"z","type":"video"},{"id":"a","type":"image"},{"id":"ignored","type":"unknown"}]}`)
	}))
	defer s.Close()
	root := s.URL + "/relay/v1"
	models, err := FetchMediaModels(context.Background(), s.Client(), "Bearer test-only", root, "video")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].DisplayName != "Synthetic Z" || models[1].Media.Privacy != "private" || len(models[1].Media.Durations) != 3 {
		t.Fatalf("%+v", models)
	}
	if err := StoreMediaModels(root, "video", models); err != nil {
		t.Fatal(err)
	}
	if len(CachedMediaModels(BaseURL)) != 0 || len(CachedMediaModels(root+"/")) != 2 {
		t.Fatal("cache root leak")
	}
	resolution := "4k"
	if err := ValidateVideoOptions(root, "z", "6s", &resolution); err != nil {
		t.Fatal(err)
	}
	resolution = "720p"
	if err := ValidateVideoOptions(root, "z", "5s", &resolution); err == nil {
		t.Fatal("accepted known invalid constraints")
	}
	if err := ValidateVideoOptions(root, "unknown", "5s", &resolution); err != nil {
		t.Fatal(err)
	}
}
func TestCachePerTypeFreshness(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := "https://synthetic.example/relay"
	old := time.Now().Add(-cache.ModelCacheTTL - time.Minute)
	if err := cache.WriteModelInfoCache(MediaCacheKey(root), []cache.CachedModel{{ID: "old-video", Type: "video", Media: &cache.MediaModelMetadata{FetchedAt: old, Durations: []string{"4s"}}}}); err != nil {
		t.Fatal(err)
	}
	if err := StoreMediaModels(root, "image", []cache.CachedModel{{ID: "fresh-image", Type: "image"}}); err != nil {
		t.Fatal(err)
	}
	fresh := CachedMediaModels(root)
	if len(fresh) != 1 || fresh[0].ID != "fresh-image" {
		t.Fatalf("unrelated type freshened: %v", fresh)
	}
	stored, err := cache.ReadModelCache(MediaCacheKey(root))
	if err != nil || len(stored.ModelInfos) != 2 {
		t.Fatalf("lost other type: %+v %v", stored, err)
	}
	if err := ValidateVideoOptions(root, "old-video", "8s", nil); err != nil {
		t.Fatal("stale rejected", err)
	}
}
func TestCatalogRefusesRedirectAndBounds(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	for _, kind := range []string{"redirect", "oversize", "malformed", "status"} {
		t.Run(kind, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "redirect":
					http.Redirect(w, r, target.URL, 307)
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", int(MaxCatalogBytes+1)))
				case "malformed":
					fmt.Fprint(w, `{"data":`)
				case "status":
					http.Error(w, "synthetic error", 401)
				}
			}))
			defer s.Close()
			if _, err := FetchMediaModels(context.Background(), s.Client(), "test-only", s.URL, "image"); err == nil {
				t.Fatal("accepted invalid catalog")
			}
		})
	}
	if hits != 0 {
		t.Fatal("followed redirect")
	}
}
