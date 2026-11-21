package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkForward compares the same local upstream and payload over real HTTP.
// Proxy runs include real WAL/NORMAL start/completion writes. These microbenchmarks
// are not provider latency, container RSS, TTFB or sustained disk-growth claims.
func BenchmarkForward(b *testing.B) {
	const delta = `data: {"type":"response.output_text.delta","delta":"hello there","output_index":0,"content_index":0}` + "\n\n"
	const terminal = `data: {"type":"response.completed","response":{"id":"r1","model":"fixture","usage":{"input_tokens":120,"output_tokens":256}}}` + "\n\n"
	for _, fixture := range []struct {
		name, content, path, usage string
		body                       []byte
	}{
		{"JSON", "application/json", "/opaque", "", []byte(`{"ok":true,"opaque":"not inspected"}`)},
		{"SSE", "text/event-stream", "/opaque", "", bytes.Repeat([]byte("data: opaque\n\n"), 16)},
		{"Binary", "application/octet-stream", "/opaque", "", bytes.Repeat([]byte{0, 1, 255, 42}, 16<<10)},
		{"Models512K", "application/json", "/v1/models", "auto", []byte(`{"data":[{"id":"demo","description":"` + strings.Repeat("x", 512<<10) + `"}]}`)},
		{"ResponseSSE256", "text/event-stream", "/v1/responses", "auto", []byte(strings.Repeat(delta, 256) + terminal)},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", fixture.content)
				w.Write(fixture.body)
				if fixture.content == "text/event-stream" {
					w.(http.Flusher).Flush()
				}
			}))
			defer up.Close()
			dir := b.TempDir()
			admin := filepath.Join(dir, "admin")
			if err := os.WriteFile(admin, bytes.Repeat([]byte("a"), 64), 0600); err != nil {
				b.Fatal(err)
			}
			s, err := Open(Options{Auth: "token", StateDir: filepath.Join(dir, "state"), AdminTokenFile: admin})
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			r := route("fixture", "/fixture/", up.URL, "X-Local", "fixture-key")
			r.Usage = fixture.usage
			snap, err := compile(fixtureProviders(Config{Routes: []Route{r}}), s.snap.revision+1, map[string]material{"fixture-key": boundMaterial(b, up.URL, "benchmark-secret")})
			if err != nil {
				b.Fatal(err)
			}
			if err = s.store.saveConfig(snap, false); err != nil {
				b.Fatal(err)
			}
			s.snap = snap
			_, token, err := s.store.createCaller("benchmark", []string{"fixture"}, snap)
			if err != nil {
				b.Fatal(err)
			}
			server := httptest.NewServer(s)
			defer server.Close()
			client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true}}
			defer client.CloseIdleConnections()
			for _, target := range []struct {
				name, url string
				proxy     bool
			}{{"Direct", up.URL + fixture.path, false}, {"ProxyAudited", server.URL + "/fixture" + fixture.path, true}} {
				b.Run(target.name, func(b *testing.B) {
					payload := bytes.Repeat([]byte("opaque"), 171)
					send := func() {
						req, _ := http.NewRequest("POST", target.url, bytes.NewReader(payload))
						if target.proxy {
							req.Header.Set("X-Local", "Local "+token)
						} else {
							req.Header.Set("X-Upstream-Key", "Real benchmark-secret")
						}
						resp, err := client.Do(req)
						if err != nil {
							b.Fatal(err)
						}
						_, err = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						if err != nil || resp.StatusCode != 200 {
							b.Fatalf("response %d %v", resp.StatusCode, err)
						}
					}
					send()
					b.ReportAllocs()
					b.SetBytes(int64(len(fixture.body)))
					b.ResetTimer()
					for b.Loop() {
						send()
					}
				})
			}
		})
	}
}
