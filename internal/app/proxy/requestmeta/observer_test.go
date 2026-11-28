package requestmeta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func observe(t *testing.T, body, protocol, path, profile, upstream string) Metadata {
	t.Helper()
	return parse([]byte(body), protocol, path, profile, upstream)
}
func TestSettingsFamilies(t *testing.T) {
	tests := []struct {
		name, protocol, path, profile, upstream, body string
		want                                          map[string]any
	}{
		{name: "responses", protocol: "responses", upstream: "https://api.openai.com/v1", body: `{"model":"gpt-5.6","reasoning":{"effort":"high","mode":"pro","context":"current_turn","summary":"auto"},"text":{"verbosity":"low"},"multi_agent":{"enabled":false,"max_concurrent_subagents":3},"prompt_cache_options":{"mode":"explicit","ttl":"30m"},"max_output_tokens":0,"service_tier":"priority","temperature":0,"top_p":0,"stream":false,"parallel_tool_calls":false,"tool_choice":"auto","tools":[]}`, want: map[string]any{"state": "observed", "model": "gpt-5.6", "reasoning_effort": "high", "reasoning_mode": "pro", "reasoning_context": "current_turn", "reasoning_summary": "auto", "verbosity": "low", "multi_agent": false, "agent_limit": float64(3), "cache_mode": "explicit", "cache_ttl": "30m", "output_limit": float64(0), "temperature": float64(0), "top_p": float64(0), "stream": false, "parallel_tools": false, "declared_tools": float64(0)}},
		{name: "chat", protocol: "chat_completions", body: `{"model":"test","reasoning_effort":"max","max_tokens":20,"max_completion_tokens":30,"tool_choice":{"type":"function","function":{"name":"PRIVATE_TOOL"}},"tools":[{"name":"PRIVATE_TOOL","schema":{"prompt":"PRIVATE_PROMPT"}}]}`, want: map[string]any{"state": "observed", "output_limit": float64(30), "reasoning_effort": "max", "tool_choice": "function", "declared_tools": float64(1)}},
		{name: "messages", protocol: "messages", body: `{"thinking":{"type":"enabled","budget_tokens":0},"output_config":{"effort":"max"},"max_tokens":100,"tool_choice":{"type":"tool","name":"PRIVATE_TOOL","disable_parallel_tool_use":false}}`, want: map[string]any{"state": "observed", "thinking_mode": "enabled", "thinking_budget": float64(0), "reasoning_effort": "max", "parallel_tools": true, "tool_choice": "tool"}},
		{name: "gemini", protocol: "gemini", path: "/v1beta/models/gemini-test:generateContent", body: `{"generationConfig":{"thinkingConfig":{"thinkingLevel":"HIGH","thinkingBudget":-1},"maxOutputTokens":20,"temperature":0,"topP":1},"tools":[{"functionDeclarations":[{"name":"PRIVATE_TOOL"},{"name":"PRIVATE_TOOL_TWO"}]},{"googleSearch":{}}]}`, want: map[string]any{"state": "observed", "model": "gemini-test", "reasoning_effort": "HIGH", "thinking_budget_mode": "dynamic", "output_limit": float64(20), "declared_tools": float64(3)}},
		{name: "cloudcode", protocol: "gemini", path: "/v1internal:streamGenerateContent", body: `{"model":"gemini-test","project":"PRIVATE_PROJECT","request":{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}},"contents":[{"text":"PRIVATE_PROMPT"}]}}`, want: map[string]any{"state": "observed", "model": "gemini-test", "thinking_budget": float64(0)}},
		{name: "ollama", protocol: "ollama", body: `{"model":"local:test","think":false,"options":{"num_predict":-1,"temperature":0},"stream":false}`, want: map[string]any{"state": "observed", "thinking_mode": "off", "output_limit_mode": "unlimited", "stream": false}},
		{name: "ollama context", protocol: "ollama", body: `{"think":"max","options":{"num_predict":-2}}`, want: map[string]any{"state": "observed", "reasoning_effort": "max", "output_limit_mode": "context"}},
		{name: "bedrock", protocol: "bedrock", path: "/model/us.anthropic.claude-test/invoke", body: `{"model":"not-the-transport-model","thinking":{"type":"adaptive"},"max_tokens":10}`, want: map[string]any{"state": "observed", "model": "us.anthropic.claude-test", "thinking_mode": "adaptive"}},
		{name: "vllm", protocol: "chat_completions", profile: "vllm", body: `{"chat_template_kwargs":{"enable_thinking":false},"thinking_token_budget":0}`, want: map[string]any{"state": "observed", "thinking_mode": "off", "thinking_budget": float64(0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := observe(t, tt.body, tt.protocol, tt.path, tt.profile, tt.upstream)
			raw, _ := json.Marshal(m)
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			for key, want := range tt.want {
				if !reflect.DeepEqual(got[key], want) {
					t.Errorf("%s=%v want %v (%s)", key, got[key], want, raw)
				}
			}
			if bytes.Contains(raw, []byte("PRIVATE_")) {
				t.Fatalf("privacy leak: %s", raw)
			}
			if len(raw) > 4096 {
				t.Fatalf("unbounded metadata: %d", len(raw))
			}
		})
	}
}

func TestSettingsAdversarial(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"model":null}`, `{"stream":null}`, `{"stream":"false"}`, `{"temperature":-1}`, `{"top_p":1.01}`, `{"max_output_tokens":1e3}`, `{"max_output_tokens":9007199254740992}`, `{"reasoning":{"effort":"PRIVATE_ENUM"}}`,
		`{"model":"unsafe model"}`, `{"model":"` + strings.Repeat("m", 129) + `"}`, `{"model":"one","model":"two"}`, `{"input":{"x":1,"x":2}}`, `{"model":"ok"} {}`, `{"reasoning":null}`, `{"stream":false`,
		`{"input":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`,
	} {
		t.Run(body[:min(len(body), 50)], func(t *testing.T) {
			m := observe(t, body, "responses", "", "", "")
			want := "invalid"
			if body == `{}` {
				want = "observed"
			}
			if m.State != want {
				t.Fatalf("%+v want %s", m, want)
			}
		})
	}
	m := observe(t, `{"reasoning.effort":"high","Reasoning":{"effort":"max"},"reasoning_effort":"max","messages":[{"reasoning":{"effort":"low"}}]}`, "responses", "", "", "")
	if m.ReasoningEffort != "" || m.State != "observed" {
		t.Fatal(m)
	}
	for _, upstream := range []string{"https://evil.test/v1", "https://api.openai.com.evil.test/v1", "http://api.openai.com/v1"} {
		m := observe(t, `{"model":"gpt-5.6","reasoning":{"effort":"high","mode":"pro"},"multi_agent":{"enabled":true}}`, "responses", "", "", upstream)
		if m.ReasoningEffort != "high" || m.ReasoningMode != "" || m.MultiAgent != nil {
			t.Fatal(m)
		}
	}
	m = observe(t, `{"chat_template_kwargs":{"enable_thinking":true},"thinking_token_budget":99}`, "chat_completions", "", "openai-compatible", "")
	if m.ThinkingMode != "" || m.ThinkingBudget != nil {
		t.Fatal(m)
	}
	for _, body := range []string{`{"model":"gemini"}`, `{"model":"gemini","request":null}`} {
		if m := observe(t, body, "gemini", "/v1internal:generateContent", "", ""); m.State != "invalid" {
			t.Fatal(m)
		}
	}
	m = observe(t, `{"tools":[{"unsupportedTool":{"name":"PRIVATE_TOOL"}}]}`, "gemini", "/models/gemini:generateContent", "", "")
	if m.DeclaredTools != nil {
		t.Fatal(m)
	}
	body := `{"tools":[` + strings.TrimSuffix(strings.Repeat(`{},`, 257), ",") + `]}`
	if m := observe(t, body, "responses", "", "", ""); m.DeclaredTools != nil || m.State != "invalid" {
		t.Fatal(m)
	}
}

func TestProtocolSelection(t *testing.T) {
	tests := []struct{ method, path, endpoint, want string }{
		{"POST", "/custom/v1/responses", "/responses", "responses"},
		{"POST", "/v1/chat/completions", "/v1/chat/completions", "chat_completions"},
		{"GET", "/v1/responses", "/responses", ""},
		{"POST", "/other/responses", "/other/responses", ""},
		{"POST", "/v1/responses/", "/responses/", ""},
		{"POST", "/v1/models/a:generateContent", "/models/a:generateContent", "gemini"},
		{"POST", "/v1/models/a/b:generateContent", "/models/a/b:generateContent", ""},
		{"POST", "/v1internal:generateContent", "/v1internal:generateContent", "gemini"},
		{"POST", "/api/chat", "/api/chat", "ollama"},
		{"POST", "/model/anthropic.test/invoke", "/model/anthropic.test/invoke", "bedrock"},
		{"POST", "/model/other/invoke", "/model/other/invoke", "bedrock"},
		{"POST", "/model/anthropic.test/converse", "/model/anthropic.test/converse", ""},
	}
	for _, tt := range tests {
		if got := protocolFor(tt.method, tt.path, tt.endpoint); got != tt.want {
			t.Errorf("%+v got %q", tt, got)
		}
	}
}

func newObserver(body io.ReadCloser, length int64, ctx context.Context) *Observer {
	return New(body, ctx, "POST", "/v1/responses", "/responses", "openai", "https://api.openai.com/v1", http.Header{"Content-Type": {"application/json"}}, length)
}
func TestObserverCompletionAndTransparency(t *testing.T) {
	data := `{"model":"test","stream":false}`
	for _, length := range []int64{-1, int64(len(data)), int64(len(data) + 1)} {
		o := newObserver(io.NopCloser(strings.NewReader(data)), length, context.Background())
		if len(o.buf) != 0 {
			t.Fatal("read ahead")
		}
		got, err := io.ReadAll(o)
		if err != nil || string(got) != data {
			t.Fatalf("%q %v", got, err)
		}
		m := o.Finalize()
		want := "observed"
		if length > int64(len(data)) {
			want = "truncated"
		}
		if m.State != want || o.buf != nil {
			t.Fatalf("%+v buf=%d", m, len(o.buf))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := newObserver(io.NopCloser(strings.NewReader(data)), int64(len(data)), ctx)
	p := make([]byte, len(data))
	n, err := o.Read(p)
	if n != len(data) || err != nil {
		t.Fatal(n, err)
	}
	cancel() // Response cancellation does not undo fully consumed request evidence.
	if m := o.Finalize(); m.State != "observed" || m.Stream == nil || *m.Stream {
		t.Fatal(m)
	}
	if again := o.Finalize(); again.State != "observed" {
		t.Fatal(again)
	}
}

func TestObserverAbandonmentAndSkip(t *testing.T) {
	data := `{"model":"test"}`
	o := newObserver(io.NopCloser(strings.NewReader(data)), -1, context.Background())
	if m := o.Finalize(); m.State != "not_observed" {
		t.Fatal(m)
	}
	_, _ = io.Copy(io.Discard, o)
	if o.Finalize().State != "not_observed" || o.buf != nil {
		t.Fatal("late read changed frozen snapshot")
	}
	o = newObserver(io.NopCloser(strings.NewReader(data)), -1, context.Background())
	_, _ = o.Read(make([]byte, 4))
	if o.Finalize().State != "truncated" || o.buf != nil {
		t.Fatal("partial looked observed")
	}
	for _, length := range []int64{-1, MaxBytes + 1} {
		o = newObserver(io.NopCloser(strings.NewReader(strings.Repeat("x", MaxBytes+1))), length, context.Background())
		_, _ = io.Copy(io.Discard, o)
		if o.Finalize().State != "truncated" || o.buf != nil {
			t.Fatal("overbudget retained data")
		}
	}
	for _, headers := range []http.Header{
		{"Content-Type": {"multipart/form-data; boundary=x"}},
		{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}},
		{"Content-Type": {"application/json", "application/json"}},
		{"Content-Type": {"application/json"}, "Upgrade": {"websocket"}},
	} {
		o = New(io.NopCloser(strings.NewReader(data)), context.Background(), "POST", "/v1/responses", "/responses", "", "", headers, int64(len(data)))
		got, err := io.ReadAll(o)
		if err != nil || string(got) != data || o.Finalize().State != "unsupported" {
			t.Fatal(string(got), err, o.Finalize())
		}
	}
}

type gatedBody struct {
	entered, release chan struct{}
	once             sync.Once
	err              error
}

func (b *gatedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return copy(p, `{"model":"late"}`), b.err
}
func (b *gatedBody) Close() error { close(b.release); return errors.New("close failure") }
func TestObserverConcurrentFinalizeReadClose(t *testing.T) {
	body := &gatedBody{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("read failure")}
	o := newObserver(body, 16, context.Background())
	done := make(chan error, 1)
	go func() { _, err := o.Read(make([]byte, 100)); done <- err }()
	<-body.entered
	if m := o.Finalize(); m.State != "not_observed" {
		t.Fatal(m)
	}
	if err := o.Close(); err == nil || err.Error() != "close failure" {
		t.Fatal(err)
	}
	if err := <-done; err != body.err {
		t.Fatal(err)
	}
	if err := o.Close(); err == nil {
		t.Fatal("close result lost")
	}
	if m := o.Finalize(); m.State != "not_observed" || o.buf != nil {
		t.Fatal(m)
	}
}

func TestLiteralDottedKeysAndCloudModelProvenance(t *testing.T) {
	for _, body := range []string{
		`{"reasoning":{"effort":"low"},"reasoning.effort":"high"}`,
		`{"reasoning.effort":"high","reasoning":{"effort":"low"}}`,
	} {
		m := parse([]byte(body), "responses", "", "", "")
		if m.ReasoningEffort != "low" || m.State != "observed" {
			t.Fatal(m)
		}
	}
	m := parse([]byte(`{"generationConfig.thinkingConfig.thinkingLevel":"HIGH","generationConfig":{"thinkingConfig":{"thinkingLevel":"LOW"}}}`), "gemini", "/models/test:generateContent", "", "")
	if m.ReasoningEffort != "LOW" {
		t.Fatal(m)
	}
	for _, body := range []string{
		`{"model":"outer-model","request":{"model":"stray-inner-model","generationConfig":{"maxOutputTokens":5}}}`,
		`{"request":{"model":"stray-inner-model"},"model":"outer-model"}`,
	} {
		m := parse([]byte(body), "gemini", "/v1internal:generateContent", "", "")
		if m.Model != "outer-model" || m.State != "observed" {
			t.Fatal(m)
		}
	}
}

type failingBody struct {
	data          string
	err, closeErr error
}

func (b *failingBody) Read(p []byte) (int, error) { return copy(p, b.data), b.err }
func (b *failingBody) Close() error               { return b.closeErr }
func TestObserverReadAndCloseErrors(t *testing.T) {
	sentinel := errors.New("read sentinel")
	closeErr := errors.New("close sentinel")
	data := `{"model":"test"}`
	body := &failingBody{data: data, err: sentinel, closeErr: closeErr}
	observer := newObserver(body, int64(len(data)), context.Background())
	buf := make([]byte, 100)
	n, err := observer.Read(buf)
	if n != len(data) || string(buf[:n]) != data || err != sentinel {
		t.Fatal("read changed", n, err)
	}
	if err := observer.Close(); err != closeErr {
		t.Fatal("close changed", err)
	}
	if m := observer.Finalize(); m.State != "truncated" || observer.buf != nil {
		t.Fatal(m)
	}
}

func TestEscapedModelIdentity(t *testing.T) {
	model := strings.Repeat(`\u0061`, 128)
	m := parse([]byte(`{"model":"`+model+`"}`), "responses", "", "", "")
	if m.State != "observed" || m.Model != strings.Repeat("a", 128) {
		t.Fatal(m)
	}
}

func TestCloseFreezesWithoutParsing(t *testing.T) {
	data := `{"model":"test"}`
	for _, complete := range []bool{true, false} {
		o := newObserver(io.NopCloser(strings.NewReader(data)), int64(len(data)), context.Background())
		size := 4
		if complete {
			size = len(data)
		}
		_, _ = o.Read(make([]byte, size))
		if o.buf == nil {
			t.Fatal("missing transient observation")
		}
		_ = o.Close()
		// net/http must finish its body tail/trailer flush before the owner
		// parses metadata. Close freezes the evidence but cannot decode it.
		if !o.closed || o.finalized || o.meta.State != "not_observed" || o.meta.Model != "" {
			t.Fatal("transport Close parsed request settings", o.meta)
		}
		if complete && string(o.buf) != data {
			t.Fatal("complete bytes lost before owner finalization")
		}
		if !complete && o.buf != nil {
			t.Fatal("abandoned request bytes retained")
		}
		// NopCloser permits late reads. Their bytes remain transparent, but
		// they cannot upgrade the evidence frozen by Close.
		late, _ := io.ReadAll(o)
		if string(late) != data[size:] {
			t.Fatal("late forwarding changed", string(late))
		}
		want := "truncated"
		if complete {
			want = "observed"
		}
		if m := o.Finalize(); m.State != want || o.buf != nil {
			t.Fatal(m)
		}
	}
}

func TestConcurrentFinalizersShareFrozenMetadata(t *testing.T) {
	data := `{"model":"test","stream":false}`
	o := newObserver(io.NopCloser(strings.NewReader(data)), int64(len(data)), context.Background())
	_, _ = io.Copy(io.Discard, o)
	_ = o.Close()
	var results [8]Metadata
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = o.Finalize()
		}()
	}
	wg.Wait()
	for _, result := range results {
		if result.State != "observed" || result.Model != "test" || !reflect.DeepEqual(result, results[0]) {
			t.Fatal(result)
		}
	}
	if o.buf != nil {
		t.Fatal("finalized request bytes retained")
	}
}

func TestHTTP1FlushesTrailersBeforeMetadataParsing(t *testing.T) {
	data := `{"model":"test","stream":false}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != data || r.ProtoMajor != 1 || r.Trailer.Get("X-Complete") != "yes" {
			http.Error(w, "request body or trailers not forwarded", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	o := newObserver(io.NopCloser(strings.NewReader(data)), -1, context.Background())
	req, err := http.NewRequest(http.MethodPost, upstream.URL+"/responses", o)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	req.Trailer = http.Header{"X-Complete": {"yes"}}
	resp, err := upstream.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatal(resp.Status)
	}
	// The upstream received EOF and trailers: transport Close already ran,
	// but it must not have parsed settings before that final HTTP/1.1 flush.
	o.mu.Lock()
	closed, finalized, state := o.closed, o.finalized, o.meta.State
	o.mu.Unlock()
	if !closed || finalized || state != "not_observed" {
		t.Fatal("transport parsed metadata", closed, finalized, state)
	}
	if m := o.Finalize(); m.State != "observed" || m.Model != "test" {
		t.Fatal(m)
	}
}

func TestTransportModelIgnoresModelWordsInCustomRoot(t *testing.T) {
	for _, tt := range []struct{ protocol, path, want string }{
		{"gemini", "/models/gateway/v1/models/gemini-test:generateContent", "gemini-test"},
		{"bedrock", "/model/gateway/model/us.anthropic.claude-test/invoke", "us.anthropic.claude-test"},
	} {
		m := parse([]byte(`{}`), tt.protocol, tt.path, "", "")
		if m.State != "observed" || m.Model != tt.want {
			t.Fatal(m)
		}
	}
}
