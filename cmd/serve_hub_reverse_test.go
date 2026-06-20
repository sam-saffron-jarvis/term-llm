package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/samsaffron/term-llm/internal/hub"
)

func TestHubReverseNodeProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/healthz" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer node-token" {
			t.Fatalf("backend auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","agent":"artist"}`))
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHubReverseConnector(ctx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())
	waitForReverseNode(t, s, "artist")

	req := httptest.NewRequest(http.MethodGet, "/node/artist/healthz", nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"agent":"artist"`) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHubReverseNodeProxyStreamsChunkedResponse(t *testing.T) {
	large := strings.Repeat("0123456789abcdef", (hubReverseChunkSize*3)/16)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/stream" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(large))
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHubReverseConnector(ctx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())
	waitForReverseNode(t, s, "artist")

	req := httptest.NewRequest(http.MethodGet, "/node/artist/stream", nil)
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body len=%d", rec.Code, rec.Body.Len())
	}
	if rec.Body.String() != large {
		t.Fatalf("streamed body mismatch len=%d want=%d", rec.Body.Len(), len(large))
	}
}

func TestHubReverseResponseCancellation(t *testing.T) {
	backendCanceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/slow" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("hello"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
		close(backendCanceled)
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	connectorCtx, stopConnector := context.WithCancel(context.Background())
	defer stopConnector()
	go runHubReverseConnector(connectorCtx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())
	waitForReverseNode(t, s, "artist")

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/slow", nil).WithContext(reqCtx)
	resp, err := s.reverse.do(reqCtx, node, req)
	if err != nil {
		t.Fatalf("reverse do: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	cancelReq()
	_, err = io.ReadAll(resp.Body)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel err = %v", err)
	}
	select {
	case <-backendCanceled:
	case <-time.After(2 * time.Second):
		t.Fatalf("backend request was not canceled")
	}
}

func TestHubReverseReadLoopCancelsSlowConsumerWithoutBlockingOthers(t *testing.T) {
	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	wsURL := "ws" + strings.TrimPrefix(hubTS.URL, "http") + "/api/connect?node_id=artist"
	header := http.Header{}
	header.Set("Authorization", "Bearer node-token")
	header.Set(hubNodeIDHeader, "artist")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()
	waitForReverseNode(t, s, "artist")

	requests := make(chan hubReverseRequest, 16)
	go func() {
		for {
			var frame hubReverseRequest
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			if frame.Type == hubReverseFrameRequest {
				requests <- frame
			}
		}
	}()

	firstResult := make(chan reverseDoResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/slow-stream", nil)
		resp, err := s.reverse.do(context.Background(), node, req)
		firstResult <- reverseDoResult{resp: resp, err: err}
	}()
	firstReq := waitForReverseRequest(t, requests, "/chat/slow-stream")
	if err := conn.WriteJSON(hubReverseResponse{Type: hubReverseFrameResponseStart, ID: firstReq.ID, Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}}); err != nil {
		t.Fatalf("write first response_start: %v", err)
	}
	first := waitForReverseDoResult(t, firstResult)
	if first.err != nil {
		t.Fatalf("first reverse do: %v", first.err)
	}
	defer first.resp.Body.Close()

	// Do not read first.resp.Body. Enough body frames should fill that request's
	// queue; the hub must cancel only that slow request and keep reading frames
	// for other requests on the websocket.
	for i := 0; i < hubReversePendingBuffer+4; i++ {
		if err := conn.WriteJSON(hubReverseResponse{Type: hubReverseFrameResponseBody, ID: firstReq.ID, Body: []byte("chunk")}); err != nil {
			t.Fatalf("write slow body frame %d: %v", i, err)
		}
	}

	secondResult := make(chan reverseDoResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/healthz", nil)
		resp, err := s.reverse.do(context.Background(), node, req)
		secondResult <- reverseDoResult{resp: resp, err: err}
	}()
	secondReq := waitForReverseRequest(t, requests, "/chat/healthz")
	if err := conn.WriteJSON(hubReverseResponse{ID: secondReq.ID, Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: []byte("ok")}); err != nil {
		t.Fatalf("write second response: %v", err)
	}
	second := waitForReverseDoResult(t, secondResult)
	if second.err != nil {
		t.Fatalf("second reverse do after slow consumer: %v", second.err)
	}
	defer second.resp.Body.Close()
	body, err := io.ReadAll(second.resp.Body)
	if err != nil {
		t.Fatalf("read second body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("second body = %q", body)
	}
}

func TestHubReverseReadLoopSurvivesCanceledUndrainedStream(t *testing.T) {
	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	wsURL := "ws" + strings.TrimPrefix(hubTS.URL, "http") + "/api/connect?node_id=artist"
	header := http.Header{}
	header.Set("Authorization", "Bearer node-token")
	header.Set(hubNodeIDHeader, "artist")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()
	waitForReverseNode(t, s, "artist")

	requests := make(chan hubReverseRequest, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			var frame hubReverseRequest
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			if frame.Type == hubReverseFrameRequest {
				requests <- frame
			}
		}
	}()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan reverseDoResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/stream", nil).WithContext(firstCtx)
		resp, err := s.reverse.do(firstCtx, node, req)
		firstResult <- reverseDoResult{resp: resp, err: err}
	}()
	firstReq := waitForReverseRequest(t, requests, "/chat/stream")
	if err := conn.WriteJSON(hubReverseResponse{Type: hubReverseFrameResponseStart, ID: firstReq.ID, Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}}); err != nil {
		t.Fatalf("write first response_start: %v", err)
	}
	first := waitForReverseDoResult(t, firstResult)
	if first.err != nil {
		t.Fatalf("first reverse do: %v", first.err)
	}
	defer first.resp.Body.Close()

	// Fill the first request's per-request response channel while its pipe writer
	// is blocked on an undrained body. The read loop must unblock when the request
	// is canceled so later multiplexed requests on the same websocket still work.
	for _, chunk := range []string{"one", "two", "three"} {
		if err := conn.WriteJSON(hubReverseResponse{Type: hubReverseFrameResponseBody, ID: firstReq.ID, Body: []byte(chunk)}); err != nil {
			t.Fatalf("write first body %q: %v", chunk, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	cancelFirst()
	_ = first.resp.Body.Close()

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	secondResult := make(chan reverseDoResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/healthz", nil).WithContext(secondCtx)
		resp, err := s.reverse.do(secondCtx, node, req)
		secondResult <- reverseDoResult{resp: resp, err: err}
	}()
	secondReq := waitForReverseRequest(t, requests, "/chat/healthz")
	if err := conn.WriteJSON(hubReverseResponse{ID: secondReq.ID, Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: []byte("ok")}); err != nil {
		t.Fatalf("write second response: %v", err)
	}
	second := waitForReverseDoResult(t, secondResult)
	if second.err != nil {
		t.Fatalf("second reverse do after canceled stream: %v", second.err)
	}
	defer second.resp.Body.Close()
	body, err := io.ReadAll(second.resp.Body)
	if err != nil {
		t.Fatalf("read second body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("second body = %q", body)
	}
}

func TestHubReverseConnectionNextRequestIDIsUnique(t *testing.T) {
	var c hubReverseConnection

	const goroutines = 8
	const perGoroutine = 256

	ids := make(chan string, goroutines*perGoroutine)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ids <- c.nextRequestID()
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]struct{}, goroutines*perGoroutine)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate reverse request ID %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("unique reverse request IDs = %d, want %d", len(seen), goroutines*perGoroutine)
	}
}

type reverseDoResult struct {
	resp *http.Response
	err  error
}

func waitForReverseDoResult(t *testing.T, ch <-chan reverseDoResult) reverseDoResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for reverse response")
		return reverseDoResult{}
	}
}

func waitForReverseRequest(t *testing.T, ch <-chan hubReverseRequest, wantPath string) hubReverseRequest {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case req := <-ch:
			if req.Path == wantPath {
				return req
			}
			t.Fatalf("reverse request path = %q, want %q", req.Path, wantPath)
		case <-deadline:
			t.Fatalf("timed out waiting for reverse request %s", wantPath)
		}
	}
}

func TestHubReverseConnectorReconnectsAfterDroppedWebsocket(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/healthz" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","agent":"artist"}`))
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHubReverseConnector(ctx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())

	first := waitForReverseNodeConnection(t, s, "artist")
	assertReverseProxyContains(t, s, "/node/artist/healthz", `"agent":"artist"`)

	// Simulate a long-lived tunnel dying. The node-side connector should notice
	// the websocket read error, retry, and attach a fresh hub-side connection.
	_ = first.conn.Close()
	second := waitForReverseNodeReconnect(t, s, "artist", first)
	if second == first {
		t.Fatal("reverse connection pointer did not change after reconnect")
	}
	assertReverseProxyContains(t, s, "/node/artist/healthz", `"agent":"artist"`)
}

func TestHubReverseConnectorFailsInFlightRequestAndRecoversAfterDrop(t *testing.T) {
	slowStarted := make(chan struct{})
	slowCanceled := make(chan struct{})
	var slowStartedOnce, slowCanceledOnce sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/slow":
			slowStartedOnce.Do(func() { close(slowStarted) })
			<-r.Context().Done()
			slowCanceledOnce.Do(func() { close(slowCanceled) })
		case "/chat/healthz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","agent":"artist"}`))
		default:
			t.Fatalf("backend path = %q", r.URL.Path)
		}
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHubReverseConnector(ctx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())

	first := waitForReverseNodeConnection(t, s, "artist")
	inFlight := make(chan reverseDoResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "http://reverse.local/chat/slow", nil)
		resp, err := s.reverse.do(ctx, node, req)
		inFlight <- reverseDoResult{resp: resp, err: err}
	}()
	waitForClosed(t, slowStarted, "slow backend request to start")

	_ = first.conn.Close()
	res := waitForReverseDoResult(t, inFlight)
	if res.err == nil {
		if res.resp != nil {
			_ = res.resp.Body.Close()
		}
		t.Fatal("in-flight reverse request succeeded after tunnel drop; want error")
	}
	waitForClosed(t, slowCanceled, "slow backend request to be canceled")

	waitForReverseNodeReconnect(t, s, "artist", first)
	assertReverseProxyContains(t, s, "/node/artist/healthz", `"agent":"artist"`)
}

func TestHubReverseDelegationNodeJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/v2/jobs" {
			t.Fatalf("backend path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer node-token" {
			t.Fatalf("backend auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"job_reverse"}`))
	}))
	defer backend.Close()

	node := hub.Node{ID: "artist", Name: "Artist", Connection: "reverse", BasePath: "/chat", Token: "node-token"}
	s := newHubServer(hub.NewRegistry(fakeHubResolver{nodes: []hub.Node{node}}), nil)
	hubTS := httptest.NewServer(s.handler())
	defer hubTS.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHubReverseConnector(ctx, hubTS.URL, "artist", "node-token", backend.URL, "/chat", backend.Client())
	waitForReverseNode(t, s, "artist")

	var out struct {
		ID string `json:"id"`
	}
	if err := s.doNodeJSON(ctx, node, http.MethodPost, "/v2/jobs", map[string]string{"name": "demo"}, &out); err != nil {
		t.Fatalf("doNodeJSON: %v", err)
	}
	if out.ID != "job_reverse" {
		t.Fatalf("id = %q", out.ID)
	}
}

func assertReverseProxyContains(t *testing.T, s *hubServer, path, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d body=%q", path, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%s body = %q, want substring %q", path, rec.Body.String(), want)
	}
}

func waitForClosed(t *testing.T, ch <-chan struct{}, desc string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", desc)
	}
}

func waitForReverseNodeConnection(t *testing.T, s *hubServer, nodeID string) *hubReverseConnection {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.reverse.mu.RLock()
		c := s.reverse.conns[nodeID]
		s.reverse.mu.RUnlock()
		if c != nil {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %q did not connect", nodeID)
	return nil
}

func waitForReverseNodeReconnect(t *testing.T, s *hubServer, nodeID string, old *hubReverseConnection) *hubReverseConnection {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s.reverse.mu.RLock()
		c := s.reverse.conns[nodeID]
		s.reverse.mu.RUnlock()
		if c != nil && c != old {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %q did not reconnect", nodeID)
	return nil
}

func waitForReverseNode(t *testing.T, s *hubServer, nodeID string) {
	t.Helper()
	_ = waitForReverseNodeConnection(t, s, nodeID)
}
