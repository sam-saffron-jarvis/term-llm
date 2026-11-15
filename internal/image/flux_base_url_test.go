package image

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestFluxFactoryRoutesPollAndLeavesDownloadUncredentialed(t *testing.T) {
	download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-key") != "" {
			t.Error("credential leaked to download")
		}
		_, _ = w.Write([]byte("image"))
	}))
	defer download.Close()
	var paths []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Header.Get("x-key") != "secret" {
			t.Error("missing API key")
		}
		if r.URL.Path == "/proxy/v1/test-model" {
			fmt.Fprint(w, `{"polling_url":"https://untrusted.invalid/v1/get_result?id=123"}`)
			return
		}
		fmt.Fprintf(w, `{"status":"Ready","result":{"sample":%q}}`, download.URL+"/signed?token=1")
	}))
	defer api.Close()
	cfg := &config.Config{}
	cfg.Image.Flux.APIKey = "secret"
	cfg.Image.Flux.BaseURL = api.URL + "/proxy/v1/"
	p, err := NewImageProvider(cfg, "flux:test-model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Generate(context.Background(), GenerateRequest{Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "image" {
		t.Fatalf("data = %q", result.Data)
	}
	if len(paths) != 2 || paths[0] != "/proxy/v1/test-model" || paths[1] != "/proxy/v1/get_result?id=123" {
		t.Fatalf("paths = %v", paths)
	}
}

type fluxRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fluxRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFluxDefaultRegionalPollingURL(t *testing.T) {
	old := fluxHTTPClient
	t.Cleanup(func() { fluxHTTPClient = old })
	var urls []string
	fluxHTTPClient = &http.Client{Transport: fluxRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		urls = append(urls, r.URL.String())
		var body string
		switch len(urls) {
		case 1:
			body = `{"polling_url":"https://api.us1.bfl.ai/v1/get_result?id=abc%2B123"}`
		case 2:
			body = `{"status":"Ready","result":{"sample":"https://signed.invalid/image?sig=xyz"}}`
		case 3:
			body = "image"
		default:
			t.Fatalf("unexpected request %s", r.URL)
		}
		if len(urls) <= 2 && r.Header.Get("x-key") != "secret" {
			t.Error("missing API key")
		}
		if len(urls) == 3 && r.Header.Get("x-key") != "" {
			t.Error("key leaked to signed download")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	p, err := NewImageProvider(&config.Config{Image: config.ImageConfig{Flux: config.ImageFluxConfig{APIKey: "secret"}}}, "flux:test-model")
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Generate(context.Background(), GenerateRequest{Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "image" || len(urls) != 3 || urls[1] != "https://api.us1.bfl.ai/v1/get_result?id=abc%2B123" || urls[2] != "https://signed.invalid/image?sig=xyz" {
		t.Fatalf("urls %v, data %q", urls, result.Data)
	}
}

func TestFluxDefaultRejectsUntrustedPollURL(t *testing.T) {
	for _, u := range []string{"http://api.bfl.ai/v1/get_result?id=x", "https://api.bfl.ai.attacker.test/v1/get_result?id=x", "https://attacker.test/v1/get_result?id=x", "https://api.bfl.ai:444/v1/get_result?id=x", "https://user@api.bfl.ai/v1/get_result?id=x", "https://api.bfl.ai/v1/other?id=x", "https://api.bfl.ai/v1/get_result#fragment"} {
		t.Run(u, func(t *testing.T) {
			if _, err := NewFluxProvider("secret", "").pollURL(u); err == nil {
				t.Fatalf("accepted %q", u)
			}
		})
	}
}

func TestFluxAuthenticatedRedirectCannotLeakKey(t *testing.T) {
	received := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true; w.WriteHeader(http.StatusOK) }))
	defer destination.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/test-model" {
			fmt.Fprint(w, `{"polling_url":"https://api.bfl.ai/v1/get_result?id=123"}`)
			return
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer api.Close()
	cfg := &config.Config{}
	cfg.Image.Flux.APIKey = "secret"
	cfg.Image.Flux.BaseURL = api.URL + "/proxy"
	p, err := NewImageProvider(cfg, "flux:test-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Generate(context.Background(), GenerateRequest{Prompt: "test"}); err == nil {
		t.Fatal("expected redirect rejection")
	}
	if received {
		t.Fatal("redirect sent to untrusted origin")
	}
}

func TestFluxExplicitDefaultRootPinsPolling(t *testing.T) {
	old := fluxHTTPClient
	t.Cleanup(func() { fluxHTTPClient = old })
	step := 0
	fluxHTTPClient = &http.Client{Transport: fluxRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		step++
		body := "image"
		switch step {
		case 1:
			body = `{"polling_url":"https://api.us1.bfl.ai/v1/get_result?id=abc%2B123"}`
		case 2:
			if r.URL.String() != fluxBaseURL+"/get_result?id=abc%2B123" {
				t.Errorf("poll escaped explicit root: %s", r.URL)
			}
			body = `{"status":"Ready","result":{"sample":"https://signed.invalid/image?sig=xyz"}}`
		case 3:
			if r.Header.Get("x-key") != "" {
				t.Error("credential leaked to signed download")
			}
		default:
			t.Fatalf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	cfg := &config.Config{Image: config.ImageConfig{Flux: config.ImageFluxConfig{APIKey: "fake", BaseURL: fluxBaseURL + "/"}}}
	p, err := NewImageProvider(cfg, "flux:test-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Generate(context.Background(), GenerateRequest{Prompt: "test"}); err != nil {
		t.Fatal(err)
	}
	if step != 3 {
		t.Fatalf("requests=%d", step)
	}
}
