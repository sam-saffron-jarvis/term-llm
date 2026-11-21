package uploads

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	linkedChunkBytes = 32 << 10
	linkedMaxBytes   = 2 * MaxAttachmentBytes
	linkedMaxCount   = 8
	linkedIdle       = 2 * time.Minute
	offsetHeader     = "X-Term-LLM-Upload-Offset"
	sizeHeader       = "X-Term-LLM-Upload-Size"
	tokenHeader      = "X-Term-LLM-Upload-Token"
)

// LinkedUploads holds per-server assemblies. Declared capacity, not bytes
// received so far, counts against the budget until publication returns.
type LinkedUploads struct {
	mu     sync.Mutex
	active map[string]*linkedUpload
}

type linkedUpload struct {
	device              string
	connection          <-chan struct{}
	filename, mediaType string
	data                []byte
	expires             time.Time
	timer               *time.Timer
	stopDisconnect      func() bool
}

func decimalHeader(h http.Header, key string) (int, bool) {
	values := h.Values(key)
	if len(values) != 1 {
		return 0, false
	}
	n, err := strconv.ParseUint(values[0], 10, 31)
	return int(n), err == nil
}

// HandleChunk accepts requests admitted by the linked-device ingress. lifetime
// must be the verified connection's context (not the per-request context).
func (s *LinkedUploads) HandleChunk(w http.ResponseWriter, r *http.Request, device string, lifetime context.Context, writer *ReferenceHandler, hubURL string) {
	if !sameOriginUploadRequest(r, hubURL) {
		writeOpenAIError(w, 403, "invalid_origin", "invalid upload origin")
		return
	}
	offset, ok := decimalHeader(r.Header, offsetHeader)
	if !ok || len(r.Header.Values("Content-Encoding")) != 0 || len(r.Header.Values(tokenHeader)) > 1 || len(r.Header.Values("Content-Type")) > 1 {
		writeOpenAIError(w, 400, "invalid_upload", "invalid chunk headers")
		return
	}
	chunk, err := io.ReadAll(io.LimitReader(r.Body, linkedChunkBytes+1))
	if len(chunk) > linkedChunkBytes {
		writeOpenAIError(w, 413, "invalid_upload", "upload chunk exceeds 32 KiB")
		return
	}
	if err != nil || (r.ContentLength >= 0 && int64(len(chunk)) != r.ContentLength) {
		writeOpenAIError(w, 400, "invalid_upload", "incomplete chunk body")
		return
	}
	token, u, status, err := s.accept(r, device, lifetime, offset, chunk)
	if err != nil {
		writeOpenAIError(w, status, "invalid_upload", err.Error())
		return
	}
	if u == nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"token": token})
		return
	}
	defer s.release(token)
	// Publish via the ordinary upload path (shared writer admission, metadata,
	// exact-length reader, deadline, atomic storage and reference response).
	ctx, cancel := context.WithCancel(lifetime)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	if r.Context().Err() != nil {
		cancel()
	}
	full := r.Clone(ctx)
	full.Header.Set(FilenameHeader, url.PathEscape(u.filename))
	full.Header.Set("Content-Type", u.mediaType)
	full.ContentLength = int64(len(u.data))
	full.Body = io.NopCloser(bytes.NewReader(u.data))
	writer.HandleCreate(w, full, hubURL)
}

func (s *LinkedUploads) accept(r *http.Request, device string, lifetime context.Context, offset int, chunk []byte) (string, *linkedUpload, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lifetime.Err() != nil || r.Context().Err() != nil {
		return "", nil, 403, errors.New("upload connection closed")
	}
	token := r.Header.Get(tokenHeader)
	var u *linkedUpload
	if len(r.Header.Values(tokenHeader)) == 0 {
		size, ok := decimalHeader(r.Header, sizeHeader)
		if !ok || offset != 0 || len(chunk) > size || (len(chunk) == 0 && size != 0) {
			return "", nil, 400, errors.New("invalid initial size, offset or body")
		}
		if size > MaxAttachmentBytes {
			return "", nil, 413, ErrUploadTooLarge
		}
		name, typ, err := uploadRequestMetadata(r)
		if err != nil {
			return "", nil, 400, err
		}
		if !s.hasCapacity(size) {
			return "", nil, 429, errors.New("too many outstanding linked uploads")
		}
		if s.active == nil {
			s.active = make(map[string]*linkedUpload)
		}
		token = rand.Text()
		u = &linkedUpload{device: device, connection: lifetime.Done(), filename: name, mediaType: typ, data: make([]byte, 0, size)}
		s.active[token] = u
	} else {
		u = s.active[token]
		if u == nil || u.device != device || u.connection != lifetime.Done() || u.timer == nil || !time.Now().Before(u.expires) {
			return "", nil, 404, errors.New("unknown upload token")
		}
		if offset != len(u.data) {
			return "", nil, 409, errors.New("unexpected upload offset")
		}
		if len(chunk) == 0 || len(chunk) > cap(u.data)-offset || len(r.Header.Values(FilenameHeader)) > 0 || len(r.Header.Values(sizeHeader)) > 0 {
			return "", nil, 400, errors.New("invalid continuation chunk")
		}
	}
	u.data = append(u.data, chunk...)
	if len(u.data) == cap(u.data) {
		if u.timer != nil {
			u.timer.Stop()
			u.timer = nil
			u.stopDisconnect()
		}
		return token, u, 0, nil
	}
	u.expires = time.Now().Add(linkedIdle)
	if u.timer == nil {
		u.stopDisconnect = context.AfterFunc(lifetime, func() { s.expire(token) })
		u.timer = time.AfterFunc(linkedIdle, func() { s.expire(token) })
	} else {
		u.timer.Reset(linkedIdle)
	}
	return token, nil, 0, nil
}

// Called with mu held; the map includes completed buffers still being written.
func (s *LinkedUploads) hasCapacity(size int) bool {
	if len(s.active) >= linkedMaxCount {
		return false
	}
	for _, u := range s.active {
		size += cap(u.data)
	}
	return size <= linkedMaxBytes
}

func (s *LinkedUploads) expire(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.active[token]
	if u == nil || u.timer == nil {
		return
	}
	select {
	case <-u.connection:
	default:
		if time.Now().Before(u.expires) {
			return
		}
	}
	u.timer.Stop()
	u.stopDisconnect()
	delete(s.active, token)
}

func (s *LinkedUploads) release(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, token)
}
