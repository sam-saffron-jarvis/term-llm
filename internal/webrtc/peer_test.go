package webrtc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// newTestPeer creates a peer wired to handler with the given basePath,
// suitable for calling dispatchRequest in unit tests.
func newTestPeer(basePath string, handler http.Handler) *peer {
	return &peer{
		cfg:     Config{BasePath: basePath, SignalingURL: "https://example.com"},
		handler: handler,
	}
}

// collectFrames runs dispatchRequest and returns all JSON frames sent.
func collectFrames(p *peer, rawFrame []byte) []responseFrame {
	var frames []responseFrame
	send := func(text string) error {
		var f responseFrame
		if err := json.Unmarshal([]byte(text), &f); err == nil {
			frames = append(frames, f)
		}
		return nil
	}
	p.dispatchRequest(context.Background(), send, rawFrame)
	return frames
}

func encodeRequest(id, method, path string, headers map[string]string, body string) []byte {
	f := requestFrame{ID: id, Method: method, Path: path, Headers: headers}
	if body != "" {
		f.Body = base64.StdEncoding.EncodeToString([]byte(body))
	}
	data, _ := json.Marshal(f)
	return data
}

// --- validPath tests ---

func TestValidPath_Allowed(t *testing.T) {
	p := newTestPeer("/ui", nil)
	for _, path := range []string{
		"/ui/v1/responses",
		"/ui/v1/models",
		"/ui/v1/sessions/abc/messages",
	} {
		if !p.validPath(path) {
			t.Errorf("expected valid path %q", path)
		}
	}
}

func TestValidPath_Rejected(t *testing.T) {
	p := newTestPeer("/ui", nil)
	for _, path := range []string{
		"/ui/v1/../etc/passwd",
		"/ui/images/secret.png",
		"/ui/",
		"/v1/responses",      // missing basePath
		"../ui/v1/responses", // traversal
		"/ui/v2/jobs",        // not under /v1/
		"",
	} {
		if p.validPath(path) {
			t.Errorf("expected invalid path %q to be rejected", path)
		}
	}
}

// --- dispatchRequest tests ---

func TestDispatchRequest_ValidPath(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello\n"))
	})
	p := newTestPeer("/ui", handler)
	raw := encodeRequest("req1", "GET", "/ui/v1/models", nil, "")
	frames := collectFrames(p, raw)

	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (headers + done), got %d: %v", len(frames), frames)
	}
	if frames[0].Type != "headers" {
		t.Errorf("first frame should be headers, got %q", frames[0].Type)
	}
	last := frames[len(frames)-1]
	if last.Type != "done" {
		t.Errorf("last frame should be done, got %q", last.Type)
	}
	if last.Status != http.StatusOK {
		t.Errorf("done status = %d, want 200", last.Status)
	}
}

func TestDispatchRequest_RejectsInvalidPath(t *testing.T) {
	p := newTestPeer("/ui", http.DefaultServeMux)
	for _, path := range []string{"/ui/v1/../etc/passwd", "/ui/images/", "/"} {
		raw := encodeRequest("req1", "GET", path, nil, "")
		frames := collectFrames(p, raw)
		if len(frames) != 1 || frames[0].Type != "done" || frames[0].Status != http.StatusBadRequest {
			t.Errorf("path %q: expected single done/400 frame, got %v", path, frames)
		}
	}
}

func TestDispatchRequest_AuthTokenForwarded(t *testing.T) {
	var gotAuth string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	p := newTestPeer("/ui", handler)
	hdrs := map[string]string{"Authorization": "Bearer secrettoken"}
	raw := encodeRequest("req2", "GET", "/ui/v1/models", hdrs, "")
	collectFrames(p, raw)

	if gotAuth != "Bearer secrettoken" {
		t.Errorf("Authorization header not forwarded: got %q", gotAuth)
	}
}

func TestDispatchRequest_AuthTokenMissing(t *testing.T) {
	// Simulate an auth middleware that rejects requests without a token.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	p := newTestPeer("/ui", handler)
	raw := encodeRequest("req3", "GET", "/ui/v1/models", nil, "")
	frames := collectFrames(p, raw)

	last := frames[len(frames)-1]
	if last.Status != http.StatusUnauthorized {
		t.Errorf("expected 401 done frame without auth token, got %d", last.Status)
	}
}

func TestDispatchRequest_BodySizeLimit(t *testing.T) {
	p := newTestPeer("/ui", http.DefaultServeMux)
	// Create a base64 string that represents more than maxFrameBytes of data.
	bigBody := strings.Repeat("A", maxFrameBytes+1)
	f := requestFrame{ID: "req4", Method: "POST", Path: "/ui/v1/responses", Body: bigBody}
	raw, _ := json.Marshal(f)
	frames := collectFrames(p, raw)

	if len(frames) != 1 || frames[0].Status != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 done frame for oversized body, got %v", frames)
	}
}

func TestDispatchRequest_BodyDecodedAndForwarded(t *testing.T) {
	const bodyContent = `{"stream":true}`
	var gotBody string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	})
	p := newTestPeer("/ui", handler)
	raw := encodeRequest("req5", "POST", "/ui/v1/responses", nil, bodyContent)
	collectFrames(p, raw)

	if gotBody != bodyContent {
		t.Errorf("body not forwarded correctly: got %q, want %q", gotBody, bodyContent)
	}
}

func TestNewPeer_InsecureSignalingURL(t *testing.T) {
	_, err := New(context.Background(), Config{
		SignalingURL: "http://example.com/signal",
	}, http.DefaultServeMux)
	if err == nil {
		t.Fatal("expected error for http:// signaling URL")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error should mention HTTPS: %v", err)
	}
}

func TestChunkStreaming(t *testing.T) {
	// Handler that writes multiple SSE lines.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-response-id", "resp-abc")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, line := range []string{
			"event: ping",
			"data: {}",
			"",
			"event: ping",
			"data: {}",
			"",
		} {
			_, _ = w.Write([]byte(line + "\n"))
			flusher.Flush()
		}
	})
	p := newTestPeer("/ui", handler)
	raw := encodeRequest("req6", "GET", "/ui/v1/responses", nil, "")
	frames := collectFrames(p, raw)

	// Expect: 1 headers frame, 6 chunk frames, 1 done frame.
	if len(frames) < 3 {
		t.Fatalf("too few frames: %v", frames)
	}
	if frames[0].Type != "headers" {
		t.Errorf("frame[0] should be headers, got %q", frames[0].Type)
	}
	if frames[0].Headers["x-response-id"] != "resp-abc" {
		t.Errorf("headers frame missing x-response-id: %v", frames[0].Headers)
	}

	var chunks []responseFrame
	for _, f := range frames {
		if f.Type == "chunk" {
			chunks = append(chunks, f)
		}
	}
	// At minimum we expect 6 SSE lines as chunks.
	if len(chunks) < 6 {
		t.Errorf("expected at least 6 chunks, got %d", len(chunks))
	}

	last := frames[len(frames)-1]
	if last.Type != "done" || last.Status != http.StatusOK {
		t.Errorf("last frame should be done/200, got %v", last)
	}
}

func TestFlushSendsHeadersWithoutBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-response-id", "resp-idle")
		w.(http.Flusher).Flush()
	})
	p := newTestPeer("/ui", handler)
	raw := encodeRequest("req7", "GET", "/ui/v1/responses/resp-idle/events", nil, "")
	frames := collectFrames(p, raw)

	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames (headers + done), got %d: %v", len(frames), frames)
	}
	if frames[0].Type != "headers" {
		t.Fatalf("frame[0] should be headers, got %q", frames[0].Type)
	}
	if frames[0].Status != http.StatusOK {
		t.Fatalf("headers status = %d, want 200", frames[0].Status)
	}
	if frames[0].Headers["content-type"] != "text/event-stream" {
		t.Fatalf("headers content-type = %q, want text/event-stream", frames[0].Headers["content-type"])
	}
	if frames[0].Headers["x-response-id"] != "resp-idle" {
		t.Fatalf("headers x-response-id = %q, want resp-idle", frames[0].Headers["x-response-id"])
	}

	last := frames[len(frames)-1]
	if last.Type != "done" || last.Status != http.StatusOK {
		t.Fatalf("last frame should be done/200, got %v", last)
	}
}

func TestFrameEncoding_RoundTrip(t *testing.T) {
	original := requestFrame{
		ID:      "test-id",
		Method:  "POST",
		Path:    "/ui/v1/responses",
		Headers: map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"},
		Body:    base64.StdEncoding.EncodeToString([]byte(`{"stream":true}`)),
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded requestFrame
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != original.ID || decoded.Method != original.Method ||
		decoded.Path != original.Path || decoded.Body != original.Body {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
	bodyBytes, _ := base64.StdEncoding.DecodeString(decoded.Body)
	if string(bodyBytes) != `{"stream":true}` {
		t.Errorf("body decode mismatch: %q", string(bodyBytes))
	}
}
