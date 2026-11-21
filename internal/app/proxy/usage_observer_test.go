package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

const observedResponseJSON = `{"type":"response.completed","response":{"id":"r","model":"exact-model","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":4},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":19}}}`

type usageTestBody struct {
	io.Reader
	closeCalls atomic.Int32
	closeErr   error
}

func (b *usageTestBody) Close() error { b.closeCalls.Add(1); return b.closeErr }
func usageResponse(path, ct string, body io.ReadCloser) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, "https://fixture.invalid"+path, nil)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: body, Request: req}
}
func TestUsageObserverBytesAndJSON(t *testing.T) {
	for _, format := range []string{"application/json", "text/event-stream", "application/x-ndjson"} {
		t.Run(format, func(t *testing.T) {
			data := observedResponseJSON
			if format == "text/event-stream" {
				data = "data: " + data + "\r\n\r\n"
			}
			if format == "application/x-ndjson" {
				data += "\n"
			}
			body := &usageTestBody{Reader: strings.NewReader(data)}
			resp := usageResponse("/v1/responses", format, body)
			var got ObservedUsage
			calls := 0
			NewUsageObserver(Route{Usage: "auto"}, resp, func(u ObservedUsage) { got = u; calls++ })
			// Tiny reads deliberately cross JSON tokens and SSE delimiters.
			var out bytes.Buffer
			chunk := make([]byte, 3)
			for {
				n, err := resp.Body.Read(chunk)
				out.Write(chunk[:n])
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls != 0 {
				t.Fatal("published before Close")
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if out.String() != data || calls != 1 || body.closeCalls.Load() != 1 {
				t.Fatal("byte mutation or lifecycle", calls, body.closeCalls.Load())
			}
			if got.Protocol != usagetap.Responses || got.Usage.Source != usagetap.Reported || got.Usage.Input != 12 || got.Usage.CachedInput != 4 || got.Usage.Reasoning != 2 {
				t.Fatal(got)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(encoded, []byte(`"protocol":"responses"`)) || !bytes.Contains(encoded, []byte(`"cached_input":4`)) || bytes.Contains(encoded, []byte(`"Input"`)) {
				t.Fatal(string(encoded))
			}
		})
	}
}
func TestUsageObserverUnknownEncoding(t *testing.T) {
	var zipped bytes.Buffer
	z := gzip.NewWriter(&zipped)
	z.Write([]byte(observedResponseJSON))
	z.Close()
	for _, encoding := range []string{"gzip", "br", "zstd", "identity, gzip"} {
		t.Run(encoding, func(t *testing.T) {
			body := &usageTestBody{Reader: bytes.NewReader(zipped.Bytes())}
			resp := usageResponse("/v1/responses", "application/json", body)
			resp.Header.Set("Content-Encoding", encoding)
			var got ObservedUsage
			NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u })
			out, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if !bytes.Equal(out, zipped.Bytes()) || got.Usage.Source != usagetap.Unknown || got.Usage.Input != -1 {
				t.Fatal(got)
			}
		})
	}
}

type usageErrorReader struct {
	data string
	err  error
}

func (r *usageErrorReader) Read(b []byte) (int, error) {
	if r.data == "" {
		return 0, r.err
	}
	n := copy(b, r.data)
	r.data = r.data[n:]
	return n, r.err
}
func TestUsageObserverCompletion(t *testing.T) {
	sentinel := errors.New("transport failed")
	for _, mode := range []string{"EOF", "n plus EOF", "read error", "cancelled", "early close", "unread close"} {
		t.Run(mode, func(t *testing.T) {
			data := "data: " + observedResponseJSON + "\n\n"
			var reader io.Reader = strings.NewReader(data)
			if mode == "read error" {
				reader = &usageErrorReader{data: data, err: sentinel}
			}
			if mode == "n plus EOF" {
				reader = &usageErrorReader{data: data, err: io.EOF}
			}
			body := &usageTestBody{Reader: reader, closeErr: sentinel}
			resp := usageResponse("/v1/responses", "text/event-stream", body)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resp.Request = resp.Request.WithContext(ctx)
			var got ObservedUsage
			NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u })
			if mode != "unread close" {
				if mode == "early close" {
					buf := make([]byte, len(data))
					n, err := resp.Body.Read(buf)
					if n != len(data) || err != nil {
						t.Fatal(n, err)
					}
				} else {
					out, err := io.ReadAll(resp.Body)
					if string(out) != data {
						t.Fatal("changed data")
					}
					if mode == "read error" && !errors.Is(err, sentinel) {
						t.Fatal(err)
					}
				}
			}
			if mode == "cancelled" {
				cancel()
			}
			if err := resp.Body.Close(); !errors.Is(err, sentinel) {
				t.Fatal("changed close error", err)
			}
			want := usagetap.Partial
			if mode == "EOF" || mode == "n plus EOF" {
				want = usagetap.Reported
			}
			if mode == "unread close" {
				want = usagetap.Unknown
			}
			if got.Usage.Source != want {
				t.Fatal(got)
			}
		})
	}
}
func TestUsageObserverOversizeAndUnknown(t *testing.T) {
	for _, tt := range []struct{ path, ct, data string }{
		{"/v1/responses", "application/json", strings.Repeat("x", usagetap.MaxEventBytes+1)},
		{"/v1/responses", "text/event-stream", "data: " + strings.Repeat("x", usagetap.MaxEventBytes) + "\n\n"},
		{"/api/chat", "application/x-ndjson", strings.Repeat("x", usagetap.MaxEventBytes+1) + "\n"},
		{"/opaque", "application/octet-stream", "opaque"},
		{"/v1/responses", "application/json", "{"},
	} {
		resp := usageResponse(tt.path, tt.ct, io.NopCloser(strings.NewReader(tt.data)))
		var got ObservedUsage
		NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u })
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if string(out) != tt.data || got.Usage.Source != usagetap.Unknown {
			t.Fatal(got)
		}
	}
}

type usageTestDuplex struct {
	usageTestBody
	written bytes.Buffer
}

func (d *usageTestDuplex) Write(b []byte) (int, error) { return d.written.Write(b) }
func observerFrame(op byte, fin bool, data []byte) []byte {
	b := []byte{op, 0}
	if fin {
		b[0] |= 128
	}
	switch {
	case len(data) < 126:
		b[1] = byte(len(data))
	case len(data) <= 65535:
		b[1] = 126
		b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	default:
		b[1] = 127
		b = binary.BigEndian.AppendUint64(b, uint64(len(data)))
	}
	return append(b, data...)
}
func TestUsageObserverWebSocket(t *testing.T) {
	data := []byte(observedResponseJSON)
	cut := len(data) / 2
	frames := observerFrame(1, false, data[:cut])
	frames = append(frames, observerFrame(9, true, []byte("ping"))...)
	frames = append(frames, observerFrame(0, true, data[cut:])...)
	frames = append(frames, observerFrame(8, true, []byte{3, 232})...)
	for _, mode := range []string{"EOF", "close without EOF", "compressed"} {
		t.Run(mode, func(t *testing.T) {
			body := &usageTestDuplex{usageTestBody: usageTestBody{Reader: bytes.NewReader(frames)}}
			resp := usageResponse("/v1/responses", "", body)
			resp.StatusCode = 101
			if mode == "compressed" {
				resp.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate")
			}
			var got ObservedUsage
			NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u })
			duplex, ok := resp.Body.(io.ReadWriteCloser)
			if !ok {
				t.Fatal("lost duplex interface")
			}
			sent := []byte{129, 128, 1, 2, 3, 4}
			if _, err := duplex.Write(sent); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(body.written.Bytes(), sent) {
				t.Fatal("changed client bytes")
			}
			var out []byte
			if mode == "close without EOF" {
				out = make([]byte, len(frames))
				n, err := io.ReadFull(duplex, out)
				if n != len(frames) || err != nil {
					t.Fatal(n, err)
				}
			} else {
				var err error
				out, err = io.ReadAll(duplex)
				if err != nil {
					t.Fatal(err)
				}
			}
			duplex.Close()
			if !bytes.Equal(out, frames) {
				t.Fatal("changed server frames")
			}
			want := usagetap.Reported
			if mode == "close without EOF" {
				want = usagetap.Partial
			}
			if mode == "compressed" {
				want = usagetap.Unknown
			}
			if got.Usage.Source != want {
				t.Fatal(got)
			}
		})
	}
}

type panicUsageTap struct{ onFinish bool }

func (p panicUsageTap) Feed([]byte) {
	if !p.onFinish {
		panic("synthetic tap panic")
	}
}
func (p panicUsageTap) Finish(bool) usagetap.Usage { panic("synthetic finish panic") }
func TestUsageObserverPanicIsolation(t *testing.T) {
	for _, finish := range []bool{false, true} {
		resp := usageResponse("/v1/responses", "application/json", io.NopCloser(strings.NewReader(observedResponseJSON)))
		var got ObservedUsage
		NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u; panic("callback panic") })
		resp.Body.(*usageObserverBody).tap = panicUsageTap{onFinish: finish}
		out, err := io.ReadAll(resp.Body)
		if err != nil || string(out) != observedResponseJSON {
			t.Fatal(err)
		}
		if err = resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if got.Usage.Source != usagetap.Unknown {
			t.Fatal(got)
		}
	}
}

type blockingUsageBody struct {
	started, stop chan struct{}
	once          sync.Once
	calls         atomic.Int32
}

func (b *blockingUsageBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.stop
	return 0, io.ErrClosedPipe
}
func (b *blockingUsageBody) Close() error {
	if b.calls.Add(1) == 1 {
		close(b.stop)
	}
	return nil
}
func TestUsageObserverConcurrentCloseUnblocksRead(t *testing.T) {
	body := &blockingUsageBody{started: make(chan struct{}), stop: make(chan struct{})}
	resp := usageResponse("/v1/responses", "text/event-stream", body)
	var calls atomic.Int32
	NewUsageObserver(Route{}, resp, func(u ObservedUsage) {
		if u.Usage.Source != usagetap.Unknown {
			t.Error(u)
		}
		calls.Add(1)
	})
	done := make(chan struct{})
	go func() { defer close(done); resp.Body.Read(make([]byte, 8)) }()
	<-body.started
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); resp.Body.Close() }()
	}
	wg.Wait()
	<-done
	if calls.Load() != 1 || body.calls.Load() != 1 {
		t.Fatal(calls.Load(), body.calls.Load())
	}
}

func usageBedrockChunk(data string) []byte {
	var headers []byte
	for _, h := range [][2]string{{":message-type", "event"}, {":event-type", "chunk"}} {
		headers = append(headers, byte(len(h[0])))
		headers = append(headers, h[0]...)
		headers = append(headers, 7)
		headers = binary.BigEndian.AppendUint16(headers, uint16(len(h[1])))
		headers = append(headers, h[1]...)
	}
	payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
	frame := binary.BigEndian.AppendUint32(nil, uint32(16+len(headers)+len(payload)))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headers)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, headers...)
	frame = append(frame, payload...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
}
func TestUsageObserverBedrock(t *testing.T) {
	event := `{"type":"message_start","message":{"model":"claude-exact","usage":{"input_tokens":5,"cache_read_input_tokens":7,"output_tokens":1}}}`
	data := usageBedrockChunk(event)
	data = append(data, usageBedrockChunk(`{"type":"message_delta","usage":{"output_tokens":8}}`)...)
	for _, tt := range []struct {
		path, ct string
		data     []byte
	}{
		{"/model/claude/invoke-with-response-stream", "application/vnd.amazon.eventstream", data},
		{"/model/claude/invoke", "application/json", []byte(`{"model":"claude-exact","usage":{"input_tokens":5,"cache_read_input_tokens":7,"output_tokens":8}}`)},
	} {
		resp := usageResponse(tt.path, tt.ct, io.NopCloser(bytes.NewReader(tt.data)))
		var got ObservedUsage
		NewUsageObserver(Route{}, resp, func(u ObservedUsage) { got = u })
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if !bytes.Equal(out, tt.data) || got.Protocol != usagetap.Bedrock || got.Usage.Source != usagetap.Reported || got.Usage.Input != 5 || got.Usage.CachedInput != 7 || got.Usage.Output != 8 {
			t.Fatal(got)
		}
	}
}

func TestUsageObserverRecordHook(t *testing.T) {
	rec := &Record{}
	resp := usageResponse("/v1/responses", "application/json", io.NopCloser(strings.NewReader(observedResponseJSON)))
	ObserveResponseUsage(Route{Usage: "auto"}, resp, rec)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if rec.Usage != nil {
		t.Fatal("published before close")
	}
	resp.Body.Close()
	if rec.Usage == nil || rec.Usage.Usage.Source != usagetap.Reported || rec.Usage.Protocol != usagetap.Responses {
		t.Fatal(rec.Usage)
	}
	ObserveResponseUsage(Route{}, nil, nil)
	NewUsageObserver(Route{}, nil, nil)
	NewUsageObserver(Route{}, &http.Response{}, nil)
}

func TestUsageObserverTierAndAmbiguityJSON(t *testing.T) {
	data := strings.Replace(observedResponseJSON, `"model":"exact-model"`, `"model":"exact-model","service_tier":"flex"`, 1)
	resp := usageResponse("/v1/responses", "application/json", io.NopCloser(strings.NewReader(data)))
	rec := &Record{}
	ObserveResponseUsage(Route{}, resp, rec)
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if rec.Usage == nil || rec.Usage.Usage.Tier != "flex" || rec.Usage.Usage.Ambiguous {
		t.Fatal(rec.Usage)
	}
	encoded, err := json.Marshal(rec.Usage)
	if err != nil || !bytes.Contains(encoded, []byte(`"tier":"flex"`)) || !bytes.Contains(encoded, []byte(`"ambiguous":false`)) {
		t.Fatal(string(encoded), err)
	}
}

// A transport-close snapshot is per connection, not one ledger request per
// response.completed event. Keep both turns and deduplicate their snapshots.
func TestUsageObserverWebSocketMultiResponseCloseAndCancel(t *testing.T) {
	first := strings.NewReplacer(`"input_tokens":12`, `"input_tokens":10`, `"output_tokens":7`, `"output_tokens":6`, `"total_tokens":19`, `"total_tokens":16`).Replace(observedResponseJSON)
	second := strings.NewReplacer(`"id":"r"`, `"id":"r2"`, `"input_tokens":12`, `"input_tokens":3`, `"output_tokens":7`, `"output_tokens":2`, `"total_tokens":19`, `"total_tokens":5`, `"cached_tokens":4`, `"cached_tokens":1`, `"reasoning_tokens":2`, `"reasoning_tokens":0`).Replace(observedResponseJSON)
	var frames []byte
	for _, event := range []string{
		`{"type":"response.created","response":{"id":"r"}}`, first, first, observedResponseJSON,
		`{"type":"response.created","response":{"id":"r2"}}`, second, observedResponseJSON, second,
	} {
		b := []byte(event)
		cut := len(b) / 2
		frames = append(frames, observerFrame(1, false, b[:cut])...)
		frames = append(frames, observerFrame(9, true, []byte("ping"))...)
		frames = append(frames, observerFrame(0, true, b[cut:])...)
	}
	frames = append(frames, observerFrame(8, true, []byte{3, 232})...)
	sentinel := errors.New("fixture transport error")
	for _, mode := range []string{"EOF", "close before EOF", "cancel before EOF", "cancel after EOF", "read error"} {
		t.Run(mode, func(t *testing.T) {
			var reader io.Reader = bytes.NewReader(frames)
			if mode == "read error" {
				reader = io.MultiReader(reader, &usageErrorReader{err: sentinel})
			}
			body := &usageTestDuplex{usageTestBody: usageTestBody{Reader: reader}}
			resp := usageResponse("/v1/responses", "", body)
			resp.StatusCode = http.StatusSwitchingProtocols
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resp.Request = resp.Request.WithContext(ctx)
			calls := 0
			var got ObservedUsage
			NewUsageObserver(Route{Usage: "auto"}, resp, func(u ObservedUsage) { got = u; calls++ })
			var out []byte
			if mode == "close before EOF" || mode == "cancel before EOF" {
				out = make([]byte, len(frames))
				if _, err := io.ReadFull(resp.Body, out); err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				out, err = io.ReadAll(resp.Body)
				if mode == "read error" {
					if !errors.Is(err, sentinel) {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "cancel") {
				cancel()
			}
			if calls != 0 {
				t.Fatal("published while streaming")
			}
			resp.Body.Close()
			resp.Body.Close()
			u := got.Usage
			if calls != 1 || body.closeCalls.Load() != 1 || !bytes.Equal(out, frames) {
				t.Fatal("byte preservation or lifecycle failure")
			}
			if u.Source != usagetap.Reported || got.Protocol != usagetap.Responses || u.Input != 15 || u.Output != 9 || u.Total != 24 || u.CachedInput != 5 || u.Reasoning != 2 || u.Turns != 2 || u.Ambiguous {
				t.Fatal(mode, got)
			}
		})
	}
}

// The documented OpenRouter fields are account-credit cost, not a generic
// currency/final-invoice contract on every OpenAI-compatible endpoint. Without
// an explicit provider-bound adapter, they must not become a monetary charge.
func TestUsageObserverDoesNotPromoteVendorCostToCharge(t *testing.T) {
	data := `{"model":"exact-model","usage":{"prompt_tokens":194,"completion_tokens":2,"total_tokens":196,"cost":0.95,"cost_details":{"upstream_inference_cost":19}}}`
	resp := usageResponse("/v1/chat/completions", "application/json", io.NopCloser(strings.NewReader(data)))
	rec := &Record{}
	ObserveResponseUsage(Route{}, resp, rec)
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if string(out) != data || rec.Usage == nil || rec.Usage.Usage.Input != 194 || rec.Usage.Usage.Source != usagetap.Reported {
		t.Fatal(rec.Usage)
	}
	encoded, err := json.Marshal(rec.Usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"cost"`, `"cost_details"`, `"charge"`, `"currency"`} {
		if bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("unverified monetary field persisted: %s", encoded)
		}
	}
}
