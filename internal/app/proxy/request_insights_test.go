package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/requestmeta"
	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

func TestRequestInsightsForwardPersistExport(t *testing.T) {
	input := `{"model":"requested-model","reasoning":{"effort":"high"},"stream":false,"max_output_tokens":0,"input":"PRIVATE_PROMPT_CANARY","prompt_cache_key":"PRIVATE_KEY_CANARY","tools":[{"name":"PRIVATE_TOOL_CANARY","parameters":{"secret":"PRIVATE_SCHEMA_CANARY"}}]}`
	output := `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"served-model","service_tier":"flex","usage":{"input_tokens":3,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":2}}}`
	for _, usage := range []string{"auto", "off"} {
		t.Run(usage, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || string(got) != input {
					t.Errorf("request bytes changed: %q %v", got, err)
				}
				if r.URL.Path != "/v1/responses" || r.Trailer.Get("X-Safe-Trailer") != "late" {
					t.Errorf("path/trailers: %s %v", r.URL.Path, r.Trailer)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, output)
			}))
			defer up.Close()
			s := testService(t)
			r := route("test", "/test/", up.URL+"/v1", "X-Caller-Key", "key")
			r.Usage = usage
			configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": {Value: "upstream-test-key"}})
			token := caller(t, s, r.Name)
			req := httptest.NewRequest("POST", "/test/responses", strings.NewReader(input))
			req.Header.Set("X-Caller-Key", "Local "+token)
			req.Header.Set("Content-Type", "application/json")
			req.Trailer = http.Header{"X-Safe-Trailer": nil}
			req.ContentLength = -1
			req.Body = &lateTrailerBody{Reader: strings.NewReader(input), trailer: req.Trailer}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			if w.Code != 200 || w.Body.String() != output {
				t.Fatalf("response changed %d %s", w.Code, w.Body.String())
			}
			rows, _, err := s.store.records(nil, 10)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			rec := rows[0]
			if usage == "auto" {
				if rec.RequestMeta == nil || rec.RequestMeta.State != "observed" || rec.RequestMeta.Model != "requested-model" || rec.RequestMeta.OutputLimit == nil || *rec.RequestMeta.OutputLimit != 0 {
					t.Fatalf("request %+v", rec.RequestMeta)
				}
				if rec.ResponseMeta == nil || rec.ResponseMeta.Status != "incomplete" || rec.ResponseMeta.IncompleteReason != "max_output_tokens" || rec.Usage == nil || rec.Usage.Usage.Model != "served-model" || rec.Usage.Usage.Tier != "flex" {
					t.Fatalf("response %+v usage %+v", rec.ResponseMeta, rec.Usage)
				}
			} else if rec.RequestMeta != nil || rec.ResponseMeta != nil {
				t.Fatal("usage off persisted settings")
			}
			data, _ := json.Marshal(rec)
			if strings.Contains(string(data), "PRIVATE_") || strings.Contains(string(data), "prompt_cache_key") || strings.Contains(string(data), "parameters") {
				t.Fatalf("privacy leak: %s", data)
			}
			cookie, csrf := loginAdmin(t, s)
			exported := adminRequest(s, "GET", "requests/export", nil, cookie, csrf)
			if exported.Code != 200 || strings.Contains(exported.Body.String(), "PRIVATE_") {
				t.Fatalf("export %d %s", exported.Code, exported.Body.String())
			}
			if usage == "auto" && (!strings.Contains(exported.Body.String(), `"request_meta"`) || !strings.Contains(exported.Body.String(), `"response_meta"`)) {
				t.Fatal("typed metadata missing from export")
			}
			// JSON-backed legacy rows require no migration and retain unobserved settings.
			var legacy Record
			if err := json.Unmarshal([]byte(`{"id":"old","outcome":"complete"}`), &legacy); err != nil || legacy.RequestMeta != nil || legacy.ResponseMeta != nil {
				t.Fatal(legacy, err)
			}
		})
	}
}

type lateTrailerBody struct {
	*strings.Reader
	trailer http.Header
}

func (b *lateTrailerBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.trailer.Set("X-Safe-Trailer", "late")
	}
	return n, err
}
func (b *lateTrailerBody) Close() error { return nil }

func TestRequestInsightsWebSocketTurnSeparation(t *testing.T) {
	connection := Record{ID: "connection", RequestMeta: &requestmeta.Metadata{State: "observed", Model: "handshake"}, ResponseMeta: &usagetap.ResponseMetadata{State: "observed", Status: "failed"}}
	turn := usagetap.WebSocketTurn{Started: time.Now(), Ended: time.Now(), Outcome: "complete", ResponseMeta: &usagetap.ResponseMetadata{State: "observed", Status: "completed"}}
	got := webSocketTurnRecord(connection, turn)
	if got.RequestMeta != nil || got.ResponseMeta == nil || got.ResponseMeta.Status != "completed" {
		t.Fatalf("inherited connection metadata: %+v", got)
	}
}

func TestRequestObservationSentinelAndUnreadCustody(t *testing.T) {
	s := testService(t)
	r := route("test", "/test/", "http://fixture.invalid/v1", "X-Caller-Key", "key")
	r.Usage = "auto"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": {Value: "test-key"}})
	ar := s.snap.routes[0]
	req := httptest.NewRequest("POST", "/test/responses", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	observer := observeRequestBody(req.Body, req, ar, "responses")
	if observer == nil || observer.Finalize().State != "not_observed" {
		t.Fatal("unread request claimed observed")
	}
	// The wrapper itself never creates a GetBody/replay facility.
	if req.GetBody != nil {
		t.Fatal("request gained replayability")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observer = requestmeta.New(io.NopCloser(strings.NewReader(`{}`)), ctx, "POST", "/v1/responses", "/responses", "", "", req.Header, -1)
	_, _ = observer.Read(make([]byte, 1))
	if m := observer.Finalize(); m.State != "truncated" {
		t.Fatal(m)
	}
}

type insightBlockedBody struct {
	entered, release chan struct{}
	once             sync.Once
}

func (b *insightBlockedBody) Read(p []byte) (int, error) {
	close(b.entered)
	<-b.release
	return copy(p, `{"model":"late-model"}`), io.EOF
}
func (b *insightBlockedBody) Close() error { b.once.Do(func() { close(b.release) }); return nil }

func TestRequestInsightsEarlyResponseWithLateRead(t *testing.T) {
	s := testService(t)
	r := route("test", "/test/", "http://fixture.invalid/v1", "X-Caller-Key", "key")
	r.Usage = "auto"
	configure(t, s, Config{Routes: []Route{r}}, map[string]material{"key": {Value: "test-key"}})
	body := &insightBlockedBody{entered: make(chan struct{}), release: make(chan struct{})}
	readDone := make(chan string, 1)
	var outgoing *http.Request
	s.transport.RegisterProtocol("http", nativeUsageTestTransport(func(req *http.Request) (*http.Response, error) {
		outgoing = req
		if req.GetBody != nil || req.Body == nil {
			t.Error("non-replayable body changed")
		}
		go func() {
			buf := make([]byte, 100)
			n, err := req.Body.Read(buf)
			if err != io.EOF {
				t.Error(err)
			}
			readDone <- string(buf[:n])
		}()
		<-body.entered
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"completed","usage":{"input_tokens":1,"output_tokens":2}}`)), Request: req}, nil
	}))
	req := httptest.NewRequest("POST", "/test/responses", nil)
	req.Body = body
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Caller-Key", "Local "+caller(t, s, r.Name))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	rows, _, err := s.store.records(nil, 10)
	if err != nil || len(rows) != 1 || rows[0].RequestMeta == nil || rows[0].RequestMeta.State != "not_observed" || rows[0].Usage == nil || rows[0].Usage.Usage.Output != 2 {
		t.Fatalf("early response %+v %v", rows, err)
	}
	_ = outgoing.Body.Close()
	if got := <-readDone; got != `{"model":"late-model"}` {
		t.Fatal("late bytes changed", got)
	}
	rows, _, err = s.store.records(nil, 10)
	if err != nil || rows[0].RequestMeta.State != "not_observed" {
		t.Fatal("late read changed terminal record", rows, err)
	}
}

func TestRequestInsightsBedrockDiagnostics(t *testing.T) {
	wire := usageBedrockChunk(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":0},"server_tool_use":{"web_search_requests":1}}}`)
	resp := usageResponse("/model/anthropic.test/invoke-with-response-stream", "application/vnd.amazon.eventstream", io.NopCloser(strings.NewReader(string(wire))))
	record := &Record{}
	ObserveResponseUsage(Route{Usage: "auto"}, resp, record)
	got, err := io.ReadAll(resp.Body)
	if err != nil || string(got) != string(wire) {
		t.Fatal("Bedrock bytes changed", err)
	}
	_ = resp.Body.Close()
	if record.ResponseMeta == nil || record.ResponseMeta.Finish != "tool_use" || record.ResponseMeta.CacheWrite5m == nil || *record.ResponseMeta.CacheWrite5m != 0 || record.Usage.Usage.Output != 5 {
		t.Fatal(record.ResponseMeta, record.Usage)
	}
}

func TestReviewRequestInsightsBedrockARNFixture(t *testing.T) {
	raw, err := os.ReadFile("../../llm/testdata/anthropic_golden/bedrock_arn_model/turn1.bedrock.request.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		RawPath string          `json:"raw_path"`
		Body    json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, anthropic := range []bool{true, false} {
		t.Run(fmt.Sprint(anthropic), func(t *testing.T) {
			body := string(fixture.Body)
			if !anthropic {
				body = strings.Replace(body, `"bedrock-2023-05-31"`, `"other-model-version"`, 1)
			}
			wire := usageBedrockChunk(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil || string(got) != body || r.URL.EscapedPath() != fixture.RawPath {
					t.Errorf("wire changed: %s %v", r.URL.EscapedPath(), err)
				}
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
				_, _ = w.Write(wire)
			}))
			defer up.Close()
			s := testService(t)
			route := route("test", "/test/", up.URL, "X-Caller-Key", "key")
			route.Usage = "auto"
			configure(t, s, Config{Routes: []Route{route}}, map[string]material{"key": {Value: "fixture-key"}})
			req := httptest.NewRequest("POST", "/test"+fixture.RawPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Caller-Key", "Local "+caller(t, s, route.Name))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, req)
			records, _, err := s.store.records(nil, 10)
			if err != nil || len(records) != 1 || w.Code != 200 || w.Body.String() != string(wire) {
				t.Fatal(records, err, w.Code)
			}
			record := records[0]
			want := "unsupported"
			if anthropic {
				want = "observed"
			}
			if record.RequestMeta == nil || record.RequestMeta.State != want || record.Usage.Usage.Output != 5 || record.ResponseMeta.Finish != "end_turn" {
				t.Fatal(record)
			}
			if anthropic && (record.RequestMeta.Model != "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/abc123" || record.RequestMeta.OutputLimit == nil || *record.RequestMeta.OutputLimit != 4096) {
				t.Fatal(record.RequestMeta)
			}
		})
	}
}
