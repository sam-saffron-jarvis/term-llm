package image

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/credentials"
)

func TestPaidImageDeadlinesAcrossProviders(t *testing.T) {
	for _, name := range []string{"openai", "openai-compatible", "chatgpt", "gemini", "flux", "xai", "openrouter", "venice"} {
		for _, op := range []string{"generate", "edit"} {
			for _, duration := range []time.Duration{0, 2 * time.Hour, 10 * time.Second} {
				t.Run(fmt.Sprintf("%s/%s/%s", name, op, duration), func(t *testing.T) {
					var provider ImageProvider
					switch name {
					case "openai":
						provider = NewOpenAIProvider("test-only", "gpt-image-2")
					case "openai-compatible":
						provider = &OpenAICompatibleProvider{name: "local", endpoint: "https://fixture.invalid/images/generations", model: "synthetic"}
					case "chatgpt":
						provider = &ChatGPTProvider{creds: &credentials.ChatGPTCredentials{AccessToken: "test-only"}, client: chatGPTImageHTTPClient, model: "synthetic", generateEndpoint: "https://fixture.invalid/images/generations", editEndpoint: "https://fixture.invalid/images/edits"}
					case "gemini":
						provider = NewGeminiProvider("test-only", "synthetic", "")
					case "flux":
						p := NewFluxProvider("test-only", "synthetic")
						p.baseURL = "https://fixture.invalid/flux"
						p.pinPolling = true
						provider = p
					case "xai":
						provider = NewXAIProvider("test-only", "synthetic")
					case "openrouter":
						provider = NewOpenRouterProvider("test-only", "synthetic")
					case "venice":
						provider = NewVeniceProvider("test-only", "synthetic", "synthetic-edit", "")
					}
					if op == "edit" && !provider.SupportsEdit() {
						t.Skip("provider does not support editing")
					}
					ctx := context.Background()
					var want time.Time
					if duration > 0 {
						var cancel context.CancelFunc
						want = time.Now().Add(duration)
						ctx, cancel = context.WithDeadline(ctx, want)
						defer cancel()
					}
					old := http.DefaultTransport
					t.Cleanup(func() { http.DefaultTransport = old })
					calls := 0
					http.DefaultTransport = openAIImageRoundTripper(func(r *http.Request) (*http.Response, error) {
						calls++
						deadline, ok := r.Context().Deadline()
						if !ok {
							t.Error("paid request has no deadline")
						} else if duration > 0 {
							if !deadline.Equal(want) {
								t.Errorf("hidden deadline cap: %s want %s", deadline, want)
							}
						} else {
							left := time.Until(deadline)
							if left > time.Hour || left < time.Hour-10*time.Second {
								t.Errorf("default budget %s", left)
							}
						}
						data := base64.StdEncoding.EncodeToString(syntheticPNG)
						body := fmt.Sprintf(`{"data":[{"b64_json":%q}],"output_format":"png"}`, data)
						switch name {
						case "gemini":
							body = fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, data)
						case "openrouter":
							body = fmt.Sprintf(`{"choices":[{"message":{"images":[{"image_url":{"url":%q}}]}}]}`, "data:image/png;base64,"+data)
						case "venice":
							if op == "edit" {
								body = string(syntheticPNG)
							} else {
								body = fmt.Sprintf(`{"images":[%q]}`, data)
							}
						case "flux":
							switch {
							case r.Method == "POST":
								body = `{"polling_url":"https://fixture.invalid/get_result?id=test"}`
							case strings.Contains(r.URL.Path, "get_result"):
								body = `{"status":"Ready","result":{"sample":"https://signed.invalid/image?token=private"}}`
							default:
								if r.Header.Get("x-key") != "" {
									t.Error("auth on signed download")
								}
								body = string(syntheticPNG)
							}
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
					})
					var err error
					if op == "generate" {
						_, err = provider.Generate(ctx, GenerateRequest{Prompt: "hello"})
					} else {
						_, err = provider.Edit(ctx, EditRequest{Prompt: "hello", InputImages: []InputImage{{Path: "input.png", Data: syntheticPNG}}})
					}
					if err != nil {
						t.Fatal(err)
					}
					if calls == 0 {
						t.Fatal("no fixture requests")
					}
				})
			}
		}
	}
}

func TestPaidImageClientsHaveNoHiddenResponseTimeout(t *testing.T) {
	for name, client := range map[string]*http.Client{"openai": openaiHTTPClient, "chatgpt": chatGPTImageHTTPClient, "gemini": geminiHTTPClient, "flux": fluxHTTPClient, "xai": xaiHTTPClient, "venice": veniceHTTPClient} {
		if client.Timeout != 0 {
			t.Errorf("%s response capped at %s", name, client.Timeout)
		}
	}
}

func TestFluxPollingPastFormerAttemptCap(t *testing.T) {
	oldTransport, oldWait := http.DefaultTransport, fluxPollWait
	t.Cleanup(func() { http.DefaultTransport = oldTransport; fluxPollWait = oldWait })
	fluxPollWait = func(context.Context, time.Duration) error { return nil }
	polls := 0
	http.DefaultTransport = openAIImageRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := `{"status":"Pending"}`
		if strings.Contains(r.URL.Path, "get_result") {
			polls++
			if polls == 62 {
				body = `{"status":"Ready","result":{"sample":"https://signed.invalid/image"}}`
			}
		} else {
			body = string(syntheticPNG)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	p := NewFluxProvider("test-only", "synthetic")
	if _, err := p.pollAndDownload(context.Background(), "https://fixture.invalid/get_result"); err != nil {
		t.Fatal(err)
	}
	if polls != 62 {
		t.Fatalf("polls %d", polls)
	}
}

func TestVeniceGenerateAcceptsLargeBase64JSON(t *testing.T) {
	data := make([]byte, 7<<20)
	copy(data, syntheticPNG)
	body := `{"images":["` + base64.StdEncoding.EncodeToString(data) + `"]}`
	if len(body) <= 8<<20 {
		t.Fatal("fixture must reproduce old bound")
	}
	old := veniceHTTPClient
	t.Cleanup(func() { veniceHTTPClient = old })
	veniceHTTPClient = &http.Client{Transport: openAIImageRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	result, err := NewVeniceProvider("test-only", "synthetic", "", "").Generate(context.Background(), GenerateRequest{Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != len(data) {
		t.Fatalf("decoded %d want %d", len(result.Data), len(data))
	}
}

type repeatedImageByte struct{}

func (repeatedImageByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func TestVeniceGenerateRejectsOversizedEncodedBody(t *testing.T) {
	old := veniceHTTPClient
	t.Cleanup(func() { veniceHTTPClient = old })
	veniceHTTPClient = &http.Client{Transport: openAIImageRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(io.LimitReader(repeatedImageByte{}, veniceEncodedImageLimit+1))}, nil
	})}
	_, err := NewVeniceProvider("test-only", "synthetic", "", "").Generate(context.Background(), GenerateRequest{})
	if err == nil || !strings.Contains(err.Error(), "paid outcome unknown") || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal(err)
	}
}
func TestVeniceDecodedBoundBeforeAllocation(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4} {
		raw := make([]byte, n)
		encoded := base64.StdEncoding.EncodeToString(raw) + "\n"
		if _, err := decodeVeniceImage(encoded, int64(n)); err != nil {
			t.Fatalf("padding boundary %d: %v", n, err)
		}
		if _, err := decodeVeniceImage(encoded, int64(n-1)); err == nil || !strings.Contains(err.Error(), "paid outcome unknown") {
			t.Fatalf("oversize decoded %d: %v", n, err)
		}
	}
}

func TestVeniceGenerateRejectsOversizedDecodedImage(t *testing.T) {
	old := veniceHTTPClient
	t.Cleanup(func() { veniceHTTPClient = old })
	// A padded base64 string encoding 64MiB+1 fits the encoded envelope cap,
	// but must be rejected before allocating the larger decoded image.
	encodedLength := int64(base64.StdEncoding.EncodedLen(int(veniceImageLimit + 1)))
	veniceHTTPClient = &http.Client{Transport: openAIImageRoundTripper(func(*http.Request) (*http.Response, error) {
		reader := io.MultiReader(strings.NewReader(`{"images":["`), io.LimitReader(repeatedBase64Byte{}, encodedLength-1), strings.NewReader(`="]}`))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(reader)}, nil
	})}
	_, err := NewVeniceProvider("test-only", "synthetic", "", "").Generate(context.Background(), GenerateRequest{})
	if err == nil || !strings.Contains(err.Error(), "decoded Venice image exceeds") || !strings.Contains(err.Error(), "paid outcome unknown") {
		t.Fatal(err)
	}
}

type repeatedBase64Byte struct{}

func (repeatedBase64Byte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	return len(p), nil
}

func TestImageLocalInterruptionDoesNotClaimRemoteCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := RequestError(cause)
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "may continue and be charged") {
			t.Fatal(err)
		}
	}
	local := errors.New("invalid option")
	if RequestError(local) != local {
		t.Fatal("local validation mislabeled as uncertain")
	}
}
