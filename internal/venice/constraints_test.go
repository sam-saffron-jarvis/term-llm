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

func TestCatalogConstraintsNormalizeWithoutPoisoningValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		known     bool
	}{
		{"strings", `{"durations":["5","6s","0.5"],"resolutions":["360p","4K"]}`, true},
		{"numbers", `{"durations":[5,6,0.5],"resolutions":["360p","4k"]}`, true},
		{"scalar", `{"durations":"5","resolutions":"4K"}`, true},
		{"unknown-object", `{"durations":{"min":4,"max":10},"resolutions":{"width":720}}`, false},
		{"unknown-entry", `{"durations":["5",{}],"resolutions":["4K",{}]}`, false},
		{"unknown-strings", `{"durations":["5","provider-dependent"],"resolutions":["4K","auto"]}`, false},
		{"unknown-whole", `[5,6]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"data":[{"id":"synthetic","type":"video","model_spec":{"constraints":%s}}]}`, tc.raw)
			}))
			defer s.Close()
			models, err := FetchMediaModels(context.Background(), s.Client(), "test-only", s.URL, "video")
			if err != nil || len(models) != 1 {
				t.Fatal(models, err)
			}
			if err := StoreMediaModels(s.URL, "video", models); err != nil {
				t.Fatal(err)
			}
			resolution := "4k"
			if err := ValidateVideoOptions(s.URL, "synthetic", "5s", &resolution); err != nil {
				t.Fatal("equivalent valid choice rejected", err)
			}
			err = ValidateVideoOptions(s.URL, "synthetic", "7s", nil)
			if tc.known != (err != nil) {
				t.Fatalf("known=%t err=%v", tc.known, err)
			}
			if tc.known && tc.name != "scalar" {
				if err := ValidateVideoOptions(s.URL, "synthetic", "0.5s", nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestLegacyCachedConstraintStringsNormalize(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := "https://fixture.invalid/relay"
	for _, values := range [][]string{{"5", "6s"}, {"5", "unknown"}} {
		models := []cache.CachedModel{{ID: "synthetic", Type: "video", Media: &cache.MediaModelMetadata{FetchedAt: time.Now(), Durations: values}}}
		if err := cache.WriteModelInfoCache(MediaCacheKey(root), models); err != nil {
			t.Fatal(err)
		}
		if err := ValidateVideoOptions(root, "synthetic", "5s", nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(values, ","), "unknown") {
			if err := ValidateVideoOptions(root, "synthetic", "8s", nil); err != nil {
				t.Fatal("partial unknown constraint poisoned cache", err)
			}
		}
	}
}
