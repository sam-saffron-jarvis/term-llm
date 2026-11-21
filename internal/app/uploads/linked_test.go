package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/synctest"
	"time"
)

func chunkRequest(data []byte, offset int, token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/linked-uploads", bytes.NewReader(data))
	r.Header.Set(offsetHeader, strconv.Itoa(offset))
	if token != "" {
		r.Header.Set(tokenHeader, token)
	}
	return r
}
func initialChunk(data []byte, size int) *http.Request {
	r := chunkRequest(data, 0, "")
	r.Header.Set(sizeHeader, strconv.Itoa(size))
	r.Header.Set(FilenameHeader, "a%20%2B.bin")
	r.Header.Set("Content-Type", "application/octet-stream")
	return r
}
func postChunk(t *testing.T, s *LinkedUploads, ctx context.Context, device string, r *http.Request, want int) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	s.HandleChunk(rec, r, device, ctx, &ReferenceHandler{}, "")
	if rec.Code != want {
		t.Fatalf("status=%d, want %d: %s", rec.Code, want, rec.Body)
	}
	return rec.Body.Bytes()
}
func acceptedToken(t *testing.T, body []byte) string {
	t.Helper()
	var reply struct{ Token string }
	if err := json.Unmarshal(body, &reply); err != nil || reply.Token == "" {
		t.Fatalf("invalid acceptance: %s", body)
	}
	return reply.Token
}

func TestLinkedUploadRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var s LinkedUploads
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	other, closeOther := context.WithCancel(t.Context())
	defer closeOther()
	raw := bytes.Repeat([]byte{0, 1, 2, 255}, linkedChunkBytes/2+1)
	token := acceptedToken(t, postChunk(t, &s, ctx, "phone", initialChunk(raw[:linkedChunkBytes], len(raw)), 202))
	next := func() *http.Request {
		return chunkRequest(raw[linkedChunkBytes:2*linkedChunkBytes], linkedChunkBytes, token)
	}
	postChunk(t, &s, ctx, "other", next(), 404)
	postChunk(t, &s, other, "phone", next(), 404)
	postChunk(t, &s, ctx, "phone", chunkRequest(raw[:1], linkedChunkBytes+1, token), 409)
	if got := acceptedToken(t, postChunk(t, &s, ctx, "phone", next(), 202)); got != token {
		t.Fatal("continuation changed token")
	}
	postChunk(t, &s, ctx, "phone", next(), 409)
	final := func() *http.Request { return chunkRequest(raw[2*linkedChunkBytes:], 2*linkedChunkBytes, token) }
	postChunk(t, &s, ctx, "phone", chunkRequest(raw[:5], 2*linkedChunkBytes, token), 400)
	var ref Reference
	if err := json.Unmarshal(postChunk(t, &s, ctx, "phone", final(), 201), &ref); err != nil {
		t.Fatal(err)
	}
	got, _, data, err := ResolveReference(ref.UploadID)
	if err != nil || got != ref || got.Filename != "a +.bin" || !bytes.Equal(raw, data) {
		t.Fatalf("stored ref=%+v, bytes=%d, err=%v", got, len(data), err)
	}
	postChunk(t, &s, ctx, "phone", final(), 404)
	postChunk(t, &s, ctx, "phone", initialChunk(nil, 0), 201)
}

func TestLinkedUploadCleanupAndLimits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s LinkedUploads
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := func(size, status int) []byte {
			return postChunk(t, &s, ctx, "phone", initialChunk([]byte{1}, size), status)
		}
		start(MaxAttachmentBytes+1, 413)
		first := acceptedToken(t, start(MaxAttachmentBytes, 202))
		start(MaxAttachmentBytes, 202)
		start(2, 429)
		time.Sleep(linkedIdle / 2)
		postChunk(t, &s, ctx, "phone", chunkRequest([]byte{2}, 1, first), 202)
		time.Sleep(linkedIdle / 2)
		synctest.Wait()
		if len(s.active) != 1 {
			t.Fatalf("idle cleanup left %d uploads", len(s.active))
		}
		start(MaxAttachmentBytes, 202)
		cancel()
		synctest.Wait()
		if len(s.active) != 0 {
			t.Fatal("disconnect did not release buffers")
		}
		ctx, cancel = context.WithCancel(t.Context())
		defer cancel()
		for range linkedMaxCount {
			start(2, 202)
		}
		start(2, 429)
		time.Sleep(linkedIdle)
		synctest.Wait()
		if len(s.active) != 0 {
			t.Fatal("idle timer did not release buffers")
		}
	})
}
func TestLinkedUploadInvalidChunks(t *testing.T) {
	var s LinkedUploads
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	postChunk(t, &s, ctx, "phone", initialChunk(make([]byte, linkedChunkBytes+1), linkedChunkBytes+1), 413)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Set(sizeHeader, "-1") }, func(r *http.Request) { r.Header.Del(offsetHeader) }, func(r *http.Request) { r.Header.Add(offsetHeader, "0") }, func(r *http.Request) { r.Header.Set(FilenameHeader, "%2Fbad") }, func(r *http.Request) { r.ContentLength++ }} {
		r := initialChunk([]byte{1}, 2)
		change(r)
		postChunk(t, &s, ctx, "phone", r, 400)
	}
	if len(s.active) != 0 {
		t.Fatal("invalid chunk allocated an upload")
	}
}
