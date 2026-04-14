package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type xaiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f xaiRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestXAIStreamStandardCloseReturnsWhenConsumerStopsDraining(t *testing.T) {
	testXAIStreamCloseReturns(t, false)
}

func TestXAIStreamWithSearchCloseReturnsWhenConsumerStopsDraining(t *testing.T) {
	testXAIStreamCloseReturns(t, true)
}

func testXAIStreamCloseReturns(t *testing.T, search bool) {
	oldClient := defaultHTTPClient
	var wroteBlockedEvent <-chan struct{}
	defaultHTTPClient = &http.Client{
		Transport: xaiRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, signal := newBlockingXAIStreamBody(search)
			wroteBlockedEvent = signal
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       body,
			}, nil
		}),
	}
	t.Cleanup(func() {
		defaultHTTPClient = oldClient
	})

	provider := NewXAIProvider("test-key", "grok-4-1-fast")
	stream, err := provider.Stream(context.Background(), Request{
		Messages: []Message{{
			Role:  RoleUser,
			Parts: []Part{{Type: PartText, Text: "hello"}},
		}},
		Search: search,
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if wroteBlockedEvent == nil {
		t.Fatal("test transport did not provide a stream body signal")
	}

	select {
	case <-wroteBlockedEvent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for xAI test stream to fill the event buffer")
	}

	closed := make(chan error, 1)
	go func() {
		closed <- stream.Close()
	}()

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() blocked after consumer stopped draining the xAI stream")
	}
}

func newBlockingXAIStreamBody(search bool) (io.ReadCloser, <-chan struct{}) {

	pr, pw := io.Pipe()
	wroteBlockedEvent := make(chan struct{})
	go func() {
		defer pw.Close()
		for i := 0; i < 17; i++ {
			var line string
			if search {
				line = fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", strings.Repeat("x", i+1))
			} else {
				line = fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", strings.Repeat("x", i+1))
			}
			if _, err := io.WriteString(pw, line); err != nil {
				return
			}
			if i == 16 {
				close(wroteBlockedEvent)
			}
		}
	}()

	return pr, wroteBlockedEvent
}
