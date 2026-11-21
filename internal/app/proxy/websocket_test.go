package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func TestWebsocketTLSHTTP1RawBytesAndLifecycle(t *testing.T) {
	for _, action := range []string{"client", "upstream", "tap", "usage", "revoke", "grants", "disable", "delete", "authority", "deadline", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			wire := []byte{0x82, 0x83, 1, 2, 3, 4, 0, 255, 19, 0x89, 0x80, 5, 6, 7, 8, 0x88, 0x80, 9, 10, 11, 12}
			replyWire := wire
			if action == "usage" {
				wire = nil
				// A large server event must pass through byte-for-byte without
				// preventing independent later turns from reaching the ledger.
				large := []byte(`{"type":"response.output_text.delta","delta":"` + strings.Repeat("x", usagetap.MaxEventBytes) + `"}`)
				replyWire = observerFrame(1, true, []byte(`{"type":"response.created","response":{"id":"r"}}`))
				replyWire = append(replyWire, observerFrame(1, true, large)...)
				replyWire = append(replyWire, observerFrame(1, true, []byte(observedResponseJSON))...)
				second := strings.ReplaceAll(observedResponseJSON, `"id":"r"`, `"id":"r2"`)
				replyWire = append(replyWire, observerFrame(1, true, []byte(`{"type":"response.created","response":{"id":"r2"}}`))...)
				replyWire = append(replyWire, observerFrame(1, true, []byte(second))...)
				replyWire = append(replyWire, observerFrame(1, true, []byte(second))...)
				replyWire = append(replyWire, observerFrame(1, true, []byte(`{"type":"response.created","response":{"id":"r3"}}`))...)
			}
			var calls atomic.Int32
			up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				wantExtensions := "permessage-deflate"
				if action == "tap" || action == "usage" {
					wantExtensions = ""
				}
				if r.Header.Get("Sec-WebSocket-Extensions") != wantExtensions {
					t.Error("extension policy")
				}
				if r.Proto != "HTTP/1.1" || r.Header.Get("Authorization") != "Real trusted" || r.Header.Get("X-Local") != "" || r.Header.Get("Sec-WebSocket-Protocol") != "responses, binary" {
					t.Errorf("upstream request: %s %v", r.Proto, r.Header)
				}
				c, rw, err := http.NewResponseController(w).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer c.Close()
				fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: binary\r\n\r\n")
				_ = rw.Flush()
				if action == "upstream" {
					_, _ = io.CopyN(c, rw, int64(len(wire)))
					return
				}
				if action == "usage" {
					_, _ = c.Write(replyWire)
					_, _ = io.Copy(io.Discard, rw)
					return
				}
				_, _ = io.Copy(c, rw)
			}))
			up.EnableHTTP2 = true
			up.StartTLS()
			defer up.Close()
			s := testService(t)
			setupCookie, setupCSRF := loginAdmin(t, s)
			if rr := adminRequest(s, "PUT", "secrets", map[string]any{"name": "key", "value": "trusted"}, setupCookie, setupCSRF); rr.Code != 200 {
				t.Fatal(rr.Body.String())
			}
			s.transport.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			s.opts.MaxConcurrent = 1
			s.slots = make(chan struct{}, 1)
			r := route("ws", "/local/", up.URL, "X-Local", "key")
			r.WebSocket = true
			if action == "usage" {
				r.Usage = "auto"
			}
			var taps, tapCloses atomic.Int32
			if action == "tap" {
				r.Usage = "auto"
				s.ResponseTap = func(_ Route, resp *http.Response, rec *Record) {
					taps.Add(1)
					duplex, ok := resp.Body.(io.ReadWriteCloser)
					if !ok {
						t.Error("tap lost duplex body")
						return
					}
					resp.Body = &testTapSocket{ReadWriteCloser: duplex, finish: func() { tapCloses.Add(1); rec.UpstreamID = "tap-finalized" }}
				}
			}
			r.SetHeaders = map[string]HeaderRule{"Authorization": {Secret: "key", Prefix: "Real "}}
			configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "trusted")})
			cinfo, token, err := s.store.createCaller("socket", []string{"ws"}, s.snap)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{}, 4)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.ServeHTTP(w, r); done <- struct{}{} }))
			defer srv.Close()
			conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if action == "deadline" {
				s.opts.RequestTimeout = 150 * time.Millisecond
			}
			fmt.Fprintf(conn, "GET /local/responses HTTP/1.1\r\nHost: local\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Protocol: responses, binary\r\nSec-WebSocket-Extensions: permessage-deflate\r\nX-Local: Local %s\r\nAuthorization: untrusted\r\n\r\n", token)
			reader := bufio.NewReader(conn)
			resp, err := http.ReadResponse(reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Protocol") != "binary" {
				t.Fatalf("handshake %v", resp)
			}
			// Masked binary, ping and close frames are opaque; no parsing/reframing.
			if _, err = conn.Write(wire); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, len(replyWire))
			if _, err = io.ReadFull(reader, got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, replyWire) {
				t.Fatalf("changed bytes: %v", got)
			}
			// No waiting admissions while a socket owns the only slot.
			if action != "upstream" {
				if rr := call(s, "GET", "/local/other", "X-Local", token, nil); rr.Code != 503 {
					t.Fatalf("full admission %d", rr.Code)
				}
			}
			if action == "usage" {
				assertSocketTurns(t, s, resp.Header.Get("X-Term-LLM-Proxy-Request-ID"), 2)
			}
			switch action {
			case "client", "tap", "usage":
				conn.Close()
			case "upstream":
			case "deadline": // The established stream must close at its request bound.
			case "shutdown":
				s.closeSockets()
			default:
				cookie, csrf := loginAdmin(t, s)
				var rr *httptest.ResponseRecorder
				switch action {
				case "revoke":
					rr = adminRequest(s, "DELETE", "callers", map[string]string{"id": cinfo.ID}, cookie, csrf)
				case "grants":
					rr = adminRequest(s, "PUT", "callers", map[string]any{"id": cinfo.ID, "grants": []string{}}, cookie, csrf)
				default:
					routes := []Route{r}
					if action == "disable" {
						routes[0].Enabled = false
					}
					if action == "delete" {
						routes = nil
					}
					if action == "authority" {
						routes[0].Allow = []string{"GET /responses"}
					}
					rr = adminRequest(s, "PUT", "config", map[string]any{"revision": s.snap.revision, "routes": routes}, cookie, csrf)
				}
				if rr.Code != 200 {
					t.Fatalf("admin %d %s", rr.Code, rr.Body.String())
				}
			}
			if action != "client" && action != "tap" && action != "usage" {
				if _, err = reader.ReadByte(); err == nil {
					t.Fatal("socket still open")
				}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handler/slot not released")
			}
			if len(s.slots) != 0 {
				t.Fatal("slot leaked")
			}
			if calls.Load() != 1 {
				t.Fatalf("replayed: %d calls", calls.Load())
			}
			if action == "usage" {
				assertSocketTurns(t, s, resp.Header.Get("X-Term-LLM-Proxy-Request-ID"), 3)
				return
			}
			records, _, err := s.store.records(nil, 50)
			if err != nil || len(records) != 1 {
				t.Fatalf("records %v %v", records, err)
			}
			rec := records[0]
			if rec.Status != 101 || rec.Sent != int64(len(wire)) || rec.Received != int64(len(replyWire)) {
				t.Fatalf("counts %+v", rec)
			}
			want := "closed_by_revocation"
			if action == "client" || action == "tap" {
				want = "closed_by_client"
			}
			if action == "upstream" {
				want = "closed_by_upstream"
			}
			if action == "tap" && (taps.Load() != 1 || tapCloses.Load() != 1 || rec.UpstreamID != "tap-finalized") {
				t.Fatalf("tap not finalized exactly once before ledger: %d %d %+v", taps.Load(), tapCloses.Load(), rec)
			}
			if action == "deadline" {
				want = "lifetime_exceeded"
			}
			if action == "shutdown" {
				want = "closed_by_shutdown"
			}
			if rec.Outcome != want {
				t.Fatalf("outcome %s want %s", rec.Outcome, want)
			}
		})
	}
}

func assertSocketTurns(t *testing.T, s *Service, connectionID string, want int) {
	t.Helper()
	if connectionID == "" {
		t.Fatal("missing handshake request ID")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		records, _, err := s.store.records(nil, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) == want {
			for i, rec := range records {
				if rec.WebSocketConnectionID != connectionID || rec.ID == connectionID || rec.Status != 101 || rec.Usage == nil || rec.Usage.Protocol != usagetap.Responses || rec.Usage.Usage.Turns != 1 || rec.Sent != -1 {
					t.Fatalf("not a single WebSocket turn: %+v", rec)
				}
				// Oldest: oversized turn. Newest after close: unfinished start.
				if i == want-1 || (want == 3 && i == 0) {
					if rec.Outcome != "incomplete" || rec.Usage.Usage.Source != usagetap.Unknown {
						t.Fatalf("unfinished turn reported known: %+v", rec)
					}
				} else if rec.Outcome != "complete" || rec.Usage.Usage.Source != usagetap.Reported || rec.Usage.Usage.Input != 12 {
					t.Fatalf("terminal usage not persisted: %+v", rec)
				}
			}
			var moneyRows, connectionRows int
			err = s.store.db.QueryRow("SELECT count(*), count(CASE WHEN request_id=? THEN 1 END) FROM money_history", connectionID).Scan(&moneyRows, &connectionRows)
			if err != nil || moneyRows != want || connectionRows != 0 {
				t.Fatalf("unexpected money rows: %d with %d for the socket, error %v", moneyRows, connectionRows, err)
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("got %d request rows, want %d", len(records), want)
		case <-tick.C:
		}
	}
}

func TestWebsocketAdmissionPolicy(t *testing.T) {
	s := testService(t)
	r := route("ws", "/local/", "https://example.invalid", "X-Local", "key")
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "trusted")})
	token := caller(t, s, "ws")
	for _, tc := range []struct {
		method, upgrade, proto string
		connection             bool
	}{{"GET", "websocket", "HTTP/1.1", true}, {"POST", "websocket", "HTTP/1.1", true}, {"GET", "h2c", "HTTP/1.1", true}, {"CONNECT", "", "HTTP/1.1", false}, {"GET", "websocket", "HTTP/2.0", true}, {"GET", "websocket", "HTTP/1.1", false}, {"GET", "", "HTTP/1.1", true}} {
		req := httptest.NewRequest(tc.method, "/local/responses", nil)
		req.Header.Set("X-Local", "Local "+token)
		req.Header.Set("Upgrade", tc.upgrade)
		if tc.connection {
			req.Header.Set("Connection", "Upgrade")
		}
		if tc.proto == "HTTP/2.0" {
			req.ProtoMajor = 2
			req.ProtoMinor = 0
		}
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Fatalf("%+v: %d", tc, rr.Code)
		}
	}
	r.WebSocket = true
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, r.Upstream, "trusted")})
	req := httptest.NewRequest("GET", "/local/responses", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-Local", "Local "+token)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("old grant widened to websocket: %d", rr.Code)
	}
}

func TestWebsocketCanceledHandshakeNoReplay(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); close(entered); <-r.Context().Done() }))
	defer up.Close()
	s := testService(t)
	r := route("ws", "/local/", up.URL, "X-Local", "key")
	r.WebSocket = true
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": boundMaterial(t, up.URL, "trusted")})
	token := caller(t, s, "ws")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/local/responses", nil).WithContext(ctx)
	req.Header.Set("X-Local", "Local "+token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	done := make(chan struct{})
	go func() { s.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled handshake stuck")
	}
	if len(s.slots) != 0 || calls.Load() != 1 {
		t.Fatal("slot leak or replay")
	}
}

type testTapSocket struct {
	io.ReadWriteCloser
	finish func()
}

func (b *testTapSocket) Close() error {
	b.finish()
	return b.ReadWriteCloser.Close()
}
