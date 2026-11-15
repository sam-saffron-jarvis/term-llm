package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
)

type imageTimeoutTransport func(*http.Request) (*http.Response, error)

func (f imageTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNativeImageToolRequestBudget(t *testing.T) {
	for _, edit := range []bool{false, true} {
		for _, duration := range []time.Duration{0, 2 * time.Hour, 10 * time.Second} {
			t.Run(fmt.Sprintf("edit=%t/%s", edit, duration), func(t *testing.T) {
				cfg := &config.Config{}
				cfg.Image.Provider = "venice"
				cfg.Image.OutputDir = t.TempDir()
				cfg.Image.Venice.APIKey = "test-only"
				cfg.Image.Venice.BaseURL = "https://fixture.invalid/relay"
				ctx := context.Background()
				var want time.Time
				if duration > 0 {
					var cancel context.CancelFunc
					want = time.Now().Add(duration)
					ctx, cancel = context.WithDeadline(ctx, want)
					defer cancel()
				}
				png := []byte("\x89PNG\r\n\x1a\nfixture")
				args := ImageGenerateArgs{Prompt: "hello"}
				if edit {
					args.InputImage = filepath.Join(cfg.Image.OutputDir, "input.png")
					if err := os.WriteFile(args.InputImage, png, 0600); err != nil {
						t.Fatal(err)
					}
				}
				old := http.DefaultTransport
				t.Cleanup(func() { http.DefaultTransport = old })
				calls := 0
				http.DefaultTransport = imageTimeoutTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("tool lacks paid deadline")
					} else if duration > 0 {
						if !deadline.Equal(want) {
							t.Error("nested tool deadline cap")
						}
					} else {
						left := time.Until(deadline)
						if left > time.Hour || left < time.Hour-10*time.Second {
							t.Errorf("tool default %s", left)
						}
					}
					body := string(png)
					if !edit {
						body = fmt.Sprintf(`{"images":[%q]}`, base64.StdEncoding.EncodeToString(png))
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				tool := NewImageGenerateTool(nil, cfg, "", nil, "", "")
				tool.serveMode = true
				raw, _ := json.Marshal(args)
				out, err := tool.Execute(ctx, raw)
				if err != nil || len(out.Images) != 1 || calls != 1 {
					t.Fatalf("%+v %v calls=%d", out, err, calls)
				}
			})
		}
	}
}
func TestNativeImageToolRefusesShortAutomaticLegacyBudgetBeforeSpend(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = imageTimeoutTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("legacy wrapper dispatched paid work")
		return nil, nil
	})
	// The trusted marker exists only on the synchronous compatibility wrapper;
	// direct/async native calls in TestNativeImageToolRequestBudget lack it.
	ctx := core.ContextWithCodeModeCell(context.Background(), struct{ core.CodeModeCell }{})
	tool := NewImageGenerateTool(nil, nil, "venice", nil, "", "")
	out, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"hello"}`))
	if err != nil || !strings.Contains(out.Content, "synchronous code_mode") || !strings.Contains(out.Content, "spawn_script") {
		t.Fatal(out, err)
	}
}
