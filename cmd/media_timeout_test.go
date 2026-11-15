package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
)

type mediaDeadlineTransport func(*http.Request) (*http.Response, error)

func (f mediaDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A timer constructed before setup calls Deadline before ready becomes true.
// This catches the old ordering without waiting for a real wizard or a timer.
type mediaSetupContext struct {
	context.Context
	t     *testing.T
	ready *bool
}

func (c mediaSetupContext) Deadline() (time.Time, bool) {
	if !*c.ready {
		c.t.Error("paid request deadline constructed before setup")
	}
	return time.Time{}, false
}

func TestImageCLIDeadlineStartsAfterSetupAndHonorsCallerChoices(t *testing.T) {
	for _, timeout := range []string{"", "2h", "10s"} {
		t.Run(timeout, func(t *testing.T) {
			resetMediaFlags(t, imageCmd)
			ready := false
			oldCtx := imageCmd.Context()
			imageCmd.SetContext(mediaSetupContext{Context: context.Background(), t: t, ready: &ready})
			t.Cleanup(func() { imageCmd.SetContext(oldCtx) })
			oldLoad := imageLoadConfig
			t.Cleanup(func() { imageLoadConfig = oldLoad })
			imageLoadConfig = func() (*config.Config, error) {
				ready = true
				cfg := &config.Config{Providers: map[string]config.ProviderConfig{"local-images": {Type: config.ProviderTypeOpenAICompat, BaseURL: "https://fixture.invalid/local/v1", Model: "synthetic"}}}
				cfg.Image.Provider = "local-images"
				return cfg, nil
			}
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			calls := 0
			http.DefaultTransport = mediaDeadlineTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if !ready {
					t.Error("request before setup")
				}
				deadline, ok := r.Context().Deadline()
				want := time.Hour
				if timeout != "" {
					want, _ = time.ParseDuration(timeout)
				}
				left := time.Until(deadline)
				if !ok || left > want || left < want-2*time.Second {
					t.Errorf("deadline %s want %s", left, want)
				}
				body := fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString([]byte("fixture")))
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			args := []string{"hello", "--no-spinner", "--output=-"}
			if timeout != "" {
				args = append(args, "--timeout="+timeout)
			}
			_, _, err := runMediaCommand(t, imageCmd, args...)
			if err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
}

func TestImageCLIDefaultDoesNotCapLongerParent(t *testing.T) {
	resetMediaFlags(t, imageCmd)
	parent, stop := context.WithTimeout(context.Background(), 2*time.Hour)
	defer stop()
	old := imageCmd.Context()
	imageCmd.SetContext(parent)
	t.Cleanup(func() { imageCmd.SetContext(old) })
	if err := imageCmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	options := captureImageOptions(imageCmd)
	ctx, cancel := options.RequestContext(parent)
	defer cancel()
	want, _ := parent.Deadline()
	got, _ := ctx.Deadline()
	if !want.Equal(got) {
		t.Fatal("CLI default capped parent")
	}
}
