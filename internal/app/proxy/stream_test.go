package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamingCancellationAndConcurrency(t *testing.T) {
	canceled := make(chan struct{})
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer up.Close()
	defer close(release)
	s := testService(t)
	s.slots = make(chan struct{}, 1)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/r/events", nil)
	req.Header.Set("X-Local", "Local "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("stream buffered or changed %q %v", line, err)
	}
	rr := call(s, "GET", "/r/second", "X-Local", token, nil)
	if rr.Code != 503 {
		t.Fatalf("concurrency %d", rr.Code)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation not propagated")
	}
	// Wait for HTTP handler completion without sleeps: Shutdown waits for active handlers.
	drain, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	if err := server.Config.Shutdown(drain); err != nil {
		t.Fatal(err)
	}
	records, _, err := s.store.records(nil, 10)
	if err != nil || len(records) != 1 || records[0].Outcome != "canceled_or_timeout" {
		t.Fatalf("cancel record %+v %v", records, err)
	}
}
func TestAmbiguousFailureNeverRetriesBody(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		io.ReadAll(r.Body)
		if r.URL.Path == "/warm" {
			io.WriteString(w, "warm")
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	for _, method := range []string{"GET", "POST"} {
		if rr := call(s, "GET", "/r/warm", "X-Local", token, nil); rr.Code != 200 {
			t.Fatal(rr.Code)
		}
		var body io.Reader
		if method == "POST" {
			body = strings.NewReader("opaque billable request")
		}
		req := httptest.NewRequest(method, "/r/billable", body)
		req.Header.Set("X-Local", "Local "+token)
		req.Header.Set("Idempotency-Key", "client-value")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 502 {
			t.Fatalf("%s ambiguous failure code=%d", method, rr.Code)
		}
	}
	if n.Load() != 4 {
		t.Fatalf("unexpected replay: total dispatches=%d", n.Load())
	}

	records, _, _ := s.store.records(nil, 10)
	if records[0].Outcome != "transport_error" {
		t.Fatal(records[0])
	}
}
func TestRedirectAndCompressionPreserved(t *testing.T) {
	body := []byte{31, 139, 8, 0, 255, 42}
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Location", "https://elsewhere.example/real-target")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(307)
		w.Write(body)
	}))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	rr := call(s, "POST", "/r/path", "X-Local", token, strings.NewReader("opaque"))
	if rr.Code != 307 || rr.Body.String() != string(body) || rr.Header().Get("Content-Encoding") != "gzip" || n.Load() != 1 {
		t.Fatalf("redirect/compressed wire changed %d %v", rr.Code, rr.Body.Bytes())
	}
}
func TestRejectedTargetsAndLimits(t *testing.T) {
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", "https://example.test/", "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, "https://example.test/", "real")})
	token := caller(t, s, "r")
	for _, target := range []string{"/r/%2e%2e/other", "/r/%252e%252e/other", "http://evil.test/r/path"} {
		rr := call(s, "POST", target, "X-Local", token, nil)
		if rr.Code != 400 {
			t.Errorf("accepted target %s %d", target, rr.Code)
		}
	}
	for _, method := range []string{"CONNECT", "GET"} {
		req := httptest.NewRequest(method, "/r/path", nil)
		req.Header.Set("X-Local", "Local "+token)
		req.Header.Set("Upgrade", "websocket")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Errorf("accepted upgrade %d", rr.Code)
		}
	}
	s.opts.MaxBody = 3
	rr := call(s, "POST", "/r/path", "X-Local", token, strings.NewReader("four"))
	if rr.Code != 413 {
		t.Fatal("body cap", rr.Code)
	}
	records, _, _ := s.store.records(url.Values{}, 50)
	if len(records) != 0 {
		t.Fatal("rejections filled ledger")
	}
}
func TestPartialStreamIsUncertain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		http.ReadRequest(bufio.NewReader(conn))
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\npartial")
	}()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", "http://"+listener.Addr().String(), "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, "http://"+listener.Addr().String(), "real")})
	token := caller(t, s, "r")
	req := httptest.NewRequest("GET", "/r/path", nil).WithContext(context.WithValue(context.Background(), http.ServerContextKey, &http.Server{}))
	req.Header.Set("X-Local", "Local "+token)
	func() {
		defer func() {
			if p := recover(); p != http.ErrAbortHandler {
				t.Errorf("partial stream panic %v", p)
			}
		}()
		s.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-done
	records, _, _ := s.store.records(nil, 10)
	if len(records) != 1 || records[0].Outcome != "incomplete" || records[0].Received != 7 {
		t.Fatalf("partial stream record %+v", records)
	}
}

func TestNativeTrailersPreserveSafeMetadataWithoutCredentials(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "opaque" {
			t.Errorf("body %q %v", body, err)
		}
		if r.Trailer.Get("X-Checksum") != "safe-checksum" || r.Trailer.Get("Authorization") != "" || r.Trailer.Get("X-Local") != "" || r.Trailer.Get("X-Upstream-Key") != "" {
			t.Errorf("trailers not filtered/preserved %v", r.Trailer)
		}
		w.Header().Set("Trailer", "X-Response-Checksum, Set-Cookie")
		io.WriteString(w, "response")
		w.Header().Set("X-Response-Checksum", "safe-response-checksum")
		w.Header().Set("Set-Cookie", "upstream=must-not-escape")
	}))
	defer up.Close()
	s := testService(t)
	configure(t, s, Config{Routes: []Route{route("r", "/r/", up.URL, "X-Local", "key")}}, map[string]material{"key": boundMaterial(t, up.URL, "real")})
	token := caller(t, s, "r")
	server := httptest.NewServer(s)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/r/path", io.NopCloser(strings.NewReader("opaque")))
	req.Header.Set("X-Local", "Local "+token)
	req.Trailer = http.Header{"X-Checksum": []string{"safe-checksum"}, "Authorization": []string{"must-not-leak"}, "X-Local": []string{"must-not-leak"}, "X-Upstream-Key": []string{"must-not-leak"}}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	if resp.Trailer.Get("X-Response-Checksum") != "safe-response-checksum" || resp.Trailer.Get("Set-Cookie") != "" {
		t.Fatal("response trailer changed", resp.Trailer)
	}
}
