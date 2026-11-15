package image

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var syntheticPNG = []byte("\x89PNG\r\n\x1a\nsynthetic")

func TestVeniceControlsAndEditTransports(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authorization")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		switch n {
		case 1:
			if r.URL.Path != "/image/generate" || body["width"] != float64(640) || body["height"] != float64(480) || body["safe_mode"] != true || body["resolution"] != nil {
				t.Errorf("generation: %s %#v", r.URL.Path, body)
			}
			fmt.Fprintf(w, `{"images":[%q]}`, base64.StdEncoding.EncodeToString(syntheticPNG))
			return
		case 2:
			if r.URL.Path != "/image/edit" || body["modelId"] != "override" || body["image"] != "https://example.com/image.png" {
				t.Errorf("single URL: %s %#v", r.URL.Path, body)
			}
		case 3:
			images, ok := body["images"].([]any)
			if !ok || len(images) != 2 || images[0] != base64.StdEncoding.EncodeToString(syntheticPNG) || images[1] != "https://example.com/image.png" {
				t.Errorf("mixed: %#v", body)
			}
		}
		w.Write(syntheticPNG)
	}))
	defer server.Close()
	p := NewVeniceProvider("test-key", "model", "configured", "1K")
	p.baseURL = server.URL
	width, height, safe := 640, 480, true
	if _, err := p.Generate(context.Background(), GenerateRequest{Venice: &VeniceControls{Width: &width, Height: &height, SafeMode: &safe}}); err != nil {
		t.Fatal(err)
	}
	url := InputImage{URL: "https://example.com/image.png"}
	if _, err := p.Edit(context.Background(), EditRequest{Model: "override", InputImages: []InputImage{url}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Edit(context.Background(), EditRequest{InputImages: []InputImage{{Data: syntheticPNG}, url}}); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("calls=%d, want 3 without discovery", n)
	}
}

func TestVeniceProcessingAndRedirect(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image/background-remove" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/image/upscale" || r.FormValue("scale") != "2" || r.FormValue("enhance") != "false" || r.FormValue("replication") != "0" {
			t.Errorf("unexpected processing form: %v", r.Form)
		}
		f, _, err := r.FormFile("image")
		if err != nil {
			t.Error(err)
		} else {
			f.Close()
		}
		w.Write(syntheticPNG)
	}))
	defer server.Close()
	p := NewVeniceProvider("test-key", "", "", "")
	p.baseURL = server.URL
	scale, enhance, replication := 2, false, 0.0
	img := InputImage{Path: "input.png", Data: syntheticPNG}
	if _, err := p.Upscale(context.Background(), UpscaleRequest{Image: img, Scale: &scale, Enhance: &enhance, Replication: &replication}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RemoveBackground(context.Background(), BackgroundRemovalRequest{Image: img}); err == nil {
		t.Fatal("redirect accepted")
	}
	if redirected {
		t.Fatal("redirect followed")
	}
	if _, err := p.Upscale(context.Background(), UpscaleRequest{Image: InputImage{URL: "https://example.com/x"}}); err == nil {
		t.Fatal("remote processing accepted")
	}
}

func TestVeniceBoundsAndValidation(t *testing.T) {
	if _, err := readVeniceBody(strings.NewReader("1234"), 3); err == nil {
		t.Fatal("oversize accepted")
	}
	p := NewVeniceProvider("test-key", "", "", "")
	width := 12
	if err := ValidateVeniceControls(p, &VeniceControls{Width: &width}, "", ""); err == nil {
		t.Fatal("unpaired dimensions accepted")
	}
	if err := ValidateEditInputs(p, []InputImage{{URL: "https://user@example.com/a"}}); err == nil {
		t.Fatal("userinfo accepted")
	}
	if err := ValidateEditInputs(NewDebugProvider(0), []InputImage{{URL: "https://example.com/a"}}); err == nil {
		t.Fatal("unsupported provider accepted URL")
	}
}

func TestVeniceDeadlineFallbackAndExplicitLongerDeadline(t *testing.T) {
	for _, operation := range []string{"generate", "edit", "upscale", "background-remove"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", operation, explicit), func(t *testing.T) {
				old := veniceHTTPClient
				t.Cleanup(func() { veniceHTTPClient = old })
				ctx := context.Background()
				var want time.Time
				if explicit {
					var cancel context.CancelFunc
					want = time.Now().Add(2 * time.Hour)
					ctx, cancel = context.WithDeadline(ctx, want)
					defer cancel()
				}
				calls := 0
				veniceHTTPClient = &http.Client{Transport: openAIImageRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("missing fallback deadline")
					} else if explicit {
						if !deadline.Equal(want) {
							t.Errorf("capped explicit deadline %s != %s", deadline, want)
						}
					} else {
						remaining := time.Until(deadline)
						if remaining < time.Hour-10*time.Second || remaining > time.Hour {
							t.Errorf("fallback deadline=%s", remaining)
						}
					}
					data := string(syntheticPNG)
					if operation == "generate" {
						data = `{"images":["` + base64.StdEncoding.EncodeToString(syntheticPNG) + `"]}`
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
				})}
				p := NewVeniceProvider("test-only", "m", "e", "1K")
				img := InputImage{Path: "local.png", Data: syntheticPNG}
				var err error
				switch operation {
				case "generate":
					_, err = p.Generate(ctx, GenerateRequest{Prompt: "hello"})
				case "edit":
					_, err = p.Edit(ctx, EditRequest{Prompt: "hello", InputImages: []InputImage{img}})
				case "upscale":
					_, err = p.Upscale(ctx, UpscaleRequest{Image: img})
				case "background-remove":
					_, err = p.RemoveBackground(ctx, BackgroundRemovalRequest{Image: img})
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
