package image

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/venice"
)

func seedVeniceSizingCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := cache.WriteModelInfoCache(venice.ImageCacheKey, []cache.CachedModel{{ID: "test-model", Type: "image", ImageConstraints: &cache.ImageModelConstraints{Resolutions: []string{"1K", "2K", "4K"}}}}); err != nil {
		t.Fatal(err)
	}
}

// Missing, fresh and stale native-size evidence must never silently change
// requested/default resolution. Metadata is not permission to discard options.
func TestVeniceSizingIndependentOfCatalog(t *testing.T) {
	for _, state := range []string{"missing", "fresh", "stale"} {
		for _, size := range []string{"", "4K", NativeSize} {
			for _, operation := range []string{"generate", "edit", "multi-edit"} {
				t.Run(state+"/"+size+"/"+operation, func(t *testing.T) {
					t.Setenv("XDG_CACHE_HOME", t.TempDir())
					at := time.Now()
					if state == "stale" {
						at = at.Add(-2 * cache.ModelCacheTTL)
					}
					if state != "missing" {
						models := []cache.CachedModel{{ID: "native", Type: "image", ImageConstraints: &cache.ImageModelConstraints{}, Media: &cache.MediaModelMetadata{FetchedAt: at}}, {ID: "native-edit", Type: "inpaint", ImageConstraints: &cache.ImageModelConstraints{}, Media: &cache.MediaModelMetadata{FetchedAt: at}}}
						if err := cache.WriteModelInfoCache(venice.ImageCacheKey, models); err != nil {
							t.Fatal(err)
						}
					}
					old := veniceHTTPClient
					t.Cleanup(func() { veniceHTTPClient = old })
					calls := 0
					veniceHTTPClient = &http.Client{Transport: openAIImageRoundTripper(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.Method != "POST" {
							t.Fatal("implicit catalog")
						}
						fields := map[string]any{}
						if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
							if err := r.ParseMultipartForm(1 << 20); err != nil {
								t.Fatal(err)
							}
							defer r.MultipartForm.RemoveAll()
							for k, v := range r.MultipartForm.Value {
								fields[k] = v[0]
							}
						} else if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
							t.Fatal(err)
						}
						want := size
						if want == "" {
							want = "2K"
						}
						if size == NativeSize {
							if _, present := fields["resolution"]; present {
								t.Errorf("native selector sent resolution %v", fields["resolution"])
							}
						} else if fields["resolution"] != want {
							t.Errorf("resolution=%v want %s", fields["resolution"], want)
						}
						data := string(syntheticPNG)
						if operation == "generate" {
							data = `{"images":["` + base64.StdEncoding.EncodeToString(syntheticPNG) + `"]}`
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
					})}
					p := NewVeniceProvider("test-only", "native", "native-edit", "2K")
					var err error
					if operation == "generate" {
						_, err = p.Generate(context.Background(), GenerateRequest{Prompt: "hello", Size: size})
					} else {
						count := 1
						if operation == "multi-edit" {
							count = 2
						}
						_, err = p.Edit(context.Background(), EditRequest{Prompt: "hello", Size: size, InputImages: make([]InputImage, count)})
					}
					if err != nil {
						t.Fatal(err)
					}
					if calls != 1 {
						t.Fatal(calls)
					}
				})
			}
		}
	}
}
