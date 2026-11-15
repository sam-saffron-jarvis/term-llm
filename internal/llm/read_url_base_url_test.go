package llm

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadURLConfiguredJinaRoot(t *testing.T) {
	original := readURLLookupIP
	readURLLookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }
	defer func() { readURLLookupIP = original }()
	called := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path != "/proxy/nested/https://example.com/article" || r.URL.Query().Get("q") != "x&y" {
			t.Errorf("URL=%s", r.URL)
		}
		io.WriteString(w, "reader content")
	}))
	defer server.Close()
	tool := NewReadURLToolWithJinaBaseURL(func() (string, error) { return server.URL + "/proxy/nested/", nil })
	tool.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "example.com" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: r}, nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	args, _ := json.Marshal(map[string]string{"url": "https://example.com/article?q=x%26y"})
	out, err := tool.Execute(context.Background(), args)
	if err != nil || out.Content != "reader content" || called != 1 {
		t.Fatalf("out=%v err=%v calls=%d", out, err, called)
	}
}
