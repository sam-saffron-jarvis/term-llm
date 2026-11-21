package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

// BenchmarkAuditedShortChat uses a fixed number of closed-loop clients, independent
// of GOMAXPROCS. The HTTP p99 measures the client round trip; elapsed time and
// pool waits include draining terminal ledger writes after the last response.
// Pool wait sums database/sql reader and writer waits, not time in the ledger queue.
func BenchmarkAuditedShortChat(b *testing.B) {
	const reply = `{"model":"fixture","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":25,"completion_tokens":35,"total_tokens":60}}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	}))
	defer up.Close()
	for _, c := range []int{1, 10} {
		b.Run(fmt.Sprintf("c=%d", c), func(b *testing.B) {
			s := testService(b)
			r := route("fixture", "/fixture/", up.URL, "X-Local", "fixture-key")
			r.Usage, r.Billing = "auto", "pay_as_you_go"
			configure(b, s, Config{Routes: []Route{r}}, map[string]material{"fixture-key": {Value: "fixture-secret"}})
			token := caller(b, s, r.Name)
			var handlers sync.WaitGroup
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlers.Add(1)
				defer handlers.Done()
				s.ServeHTTP(w, r)
			}))
			defer server.Close()
			client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: c, DisableCompression: true}, Timeout: 10 * time.Second}
			defer client.CloseIdleConnections()
			send := func() error {
				req, err := http.NewRequest("POST", server.URL+"/fixture/v1/chat/completions", strings.NewReader(`{"model":"fixture","messages":[{"role":"user","content":"hi"}]}`))
				if err != nil {
					return err
				}
				req.Header.Set("X-Local", "Local "+token)
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				n, err := io.Copy(io.Discard, resp.Body)
				if err == nil && (n != int64(len(reply)) || resp.StatusCode != http.StatusOK) {
					return fmt.Errorf("unexpected response: status %d, bytes %d", resp.StatusCode, n)
				}
				return err
			}
			run := func(n int, latencies []time.Duration) {
				var workers sync.WaitGroup
				for worker := 0; worker < min(c, n); worker++ {
					workers.Go(func() {
						for i := worker; i < n; i += c {
							start := time.Now()
							if err := send(); err != nil {
								b.Error(err)
								return
							}
							if latencies != nil {
								latencies[i] = time.Since(start)
							}
						}
					})
				}
				workers.Wait()
				handlers.Wait()
			}
			run(2*c, nil)
			if b.Failed() {
				return
			}
			latencies := make([]time.Duration, b.N)
			writeBefore, readBefore := s.store.db.Stats(), s.store.readDB.Stats()
			b.ReportAllocs()
			b.ResetTimer()
			run(b.N, latencies)
			b.StopTimer()
			writeAfter, readAfter := s.store.db.Stats(), s.store.readDB.Stats()
			var completed, moneyRows int
			if err := s.store.db.QueryRow(`SELECT (SELECT count(*) FROM requests WHERE outcome='complete'), (SELECT count(*) FROM money_history)`).Scan(&completed, &moneyRows); err != nil || completed != b.N+2*c || moneyRows != completed {
				b.Fatalf("lost ledger records: completed=%d money=%d want=%d err=%v", completed, moneyRows, b.N+2*c, err)
			}
			if b.Failed() {
				return
			}
			slices.Sort(latencies)
			b.ReportMetric(float64(latencies[(b.N-1)*99/100].Nanoseconds())/1e3, "us/p99")
			b.ReportMetric(float64((writeAfter.WaitDuration-writeBefore.WaitDuration+readAfter.WaitDuration-readBefore.WaitDuration).Nanoseconds())/1e3/float64(b.N), "db-wait-us/op")
			b.ReportMetric(float64(writeAfter.WaitCount-writeBefore.WaitCount+readAfter.WaitCount-readBefore.WaitCount)/float64(b.N), "db-waits/op")
		})
	}
}
