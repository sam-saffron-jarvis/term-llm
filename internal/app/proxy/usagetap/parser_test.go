package usagetap

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const responseJSON = `{"type":"response.completed","response":{"id":"r1","model":"gpt-test","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":4},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":19}}}`
const chatJSON = `{"model":"chat-test","usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}}`

func TestResponsesSSE(t *testing.T) {
	// SSE permits multiline JSON, CRLF and comments/other fields. Exercise every
	// two-chunk boundary and then one byte per call, not just event boundaries.
	data := "event: response.completed\r\nid: 123\r\n: comment\r\ndata: " + strings.Replace(responseJSON, `,"response":`, ",\r\ndata: \"response\":", 1) + "\r\n\r\ndata: [DONE]\r\n\r\n"
	want := Usage{Input: 12, CachedInput: 4, CacheWrite: -1, Output: 7, Reasoning: 2, Total: 19, Model: "gpt-test", Source: Reported, Turns: 1}
	for split := 0; split <= len(data); split++ {
		p := New(Responses, SSE)
		p.Feed([]byte(data[:split]))
		p.Feed([]byte(data[split:]))
		if got := p.Finish(true); got != want {
			t.Fatalf("split %d: %+v, want %+v", split, got, want)
		}
	}
	p := New(Responses, SSE)
	for i := range data {
		p.Feed([]byte{data[i]})
	}
	if got := p.Finish(true); got != want {
		t.Fatal(got)
	}
}
func TestChatCompletions(t *testing.T) {
	for _, format := range []Format{JSON, SSE, NDJSON} {
		t.Run(string(format), func(t *testing.T) {
			data := chatJSON
			if format == SSE {
				data = "data: " + data + "\n\n"
			}
			if format == NDJSON {
				data += "\n"
			}
			p := New(ChatCompletions, format)
			p.Feed([]byte(data))
			u := p.Finish(true)
			if u.Source != Reported || u.Input != 12 || u.Output != 7 || u.CachedInput != 4 || u.Reasoning != 2 {
				t.Fatal(u)
			}
		})
	}
	p := New(ChatCompletions, SSE)
	p.Feed([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
	if u := p.Finish(true); u.Source != Unknown || u.Input != -1 {
		t.Fatal(u)
	}
}
func TestMessagesStartPlusDelta(t *testing.T) {
	p := New(Messages, SSE)
	for _, data := range []string{
		`{"type":"message_start","message":{"model":"claude-test","usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":1}}}`,
		`{"type":"message_delta","usage":{"output_tokens":4}}`,
		`{"type":"message_delta","usage":{"output_tokens":9}}`,
	} {
		p.Feed([]byte("data: " + data + "\n\n"))
	}
	u := p.Finish(true)
	if u.Input != 10 || u.CachedInput != 20 || u.CacheWrite != 30 || u.Output != 9 || u.Total != -1 || u.Model != "claude-test" || u.Source != Reported {
		t.Fatal(u)
	}
	// Direct nonstreaming message has the same separate cache semantics.
	p = New(Messages, JSON)
	p.Feed([]byte(`{"type":"message","model":"claude","usage":{"input_tokens":0,"output_tokens":0}}`))
	if u = p.Finish(true); u.Input != 0 || u.Output != 0 || u.CachedInput != -1 {
		t.Fatal(u)
	}
}
func TestGeminiLastWins(t *testing.T) {
	p := New(Gemini, SSE)
	p.FeedEvent([]byte(`{"modelVersion":"gem-test","usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":8,"thoughtsTokenCount":4,"totalTokenCount":112}}`))
	p.FeedEvent([]byte(`{"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":13}}`))
	u := p.Finish(true)
	if u.Source != Reported || u.Input != 10 || u.CachedInput != 3 || u.Output != 2 || u.Reasoning != 1 || u.Total != 13 || u.Model != "gem-test" {
		t.Fatal(u)
	}
}
func TestGeminiCloudCodeWrapsUsage(t *testing.T) {
	p := ForResponse("/v1internal:streamGenerateContent", "text/event-stream")
	p.Feed([]byte("data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":4,\"totalTokenCount\":14}}}\n\n"))
	u := p.Finish(true)
	if u.Source != Reported || u.Input != 10 || u.Output != 4 || u.Total != 14 {
		t.Fatal(u)
	}
	// proto3 JSON omits zero cache-hit and thinking counts.
	if u.CachedInput != 0 || u.Reasoning != 0 {
		t.Fatal("omitted Gemini zero counts became unknown", u)
	}
}

func TestGeminiOmittedCountsNeedPromptCount(t *testing.T) {
	p := New(Gemini, JSON)
	p.Feed([]byte(`{"usageMetadata":{"candidatesTokenCount":4}}`))
	if u := p.Finish(true); u.CachedInput != -1 || u.Reasoning != -1 {
		t.Fatal("zero inferred without a prompt count", u)
	}
}

func TestOllamaFinalRecord(t *testing.T) {
	p := New(Ollama, NDJSON)
	data := `{"done":false,"eval_count":100}` + "\n" + `{"done":true,"model":"local","prompt_eval_count":6,"eval_count":3}`
	for i := range data {
		p.Feed([]byte{data[i]})
	}
	u := p.Finish(true)
	if u.Source != Reported || u.Input != 6 || u.Output != 3 || u.Total != -1 {
		t.Fatal(u)
	}
}
func TestPartialAndUnknownClassification(t *testing.T) {
	cases := []struct {
		name     string
		proto    Protocol
		data     string
		complete bool
		source   Source
		input    int64
	}{
		{"interrupted evidence", Responses, responseJSON, false, Partial, 12},
		{"no evidence", Responses, `{"type":"response.created"}`, false, Unknown, -1},
		{"unsupported", Bedrock, responseJSON, true, Unknown, -1},
		{"inapplicable", Inapplicable, `bogus`, true, NotApplicable, -1},
		{"invalid", Responses, `{`, true, Unknown, -1},
		{"incomplete provider", Responses, strings.Replace(responseJSON, "response.completed", "response.incomplete", 1), true, Partial, 12},
		{"failed provider", Responses, strings.Replace(responseJSON, "response.completed", "response.failed", 1), true, Partial, 12},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.proto, JSON)
			p.FeedEvent([]byte(tt.data))
			u := p.Finish(tt.complete)
			if u.Source != tt.source || u.Input != tt.input {
				t.Fatal(u)
			}
			p.FeedEvent([]byte(responseJSON))
			p.Feed([]byte(responseJSON))
			if p.Finish(!tt.complete) != u {
				t.Fatal("finish not stable")
			}
		})
	}
	p := New(Responses, SSE)
	p.FeedEvent([]byte(responseJSON))
	if p.Snapshot().Source != Partial {
		t.Fatal(p.Snapshot())
	}
	// A truncated event is not decoded, but previously observed evidence survives.
	p.Feed([]byte("data: {\"usage\":"))
	if u := p.Finish(false); u.Source != Partial || u.Input != 12 {
		t.Fatal(u)
	}
}
func TestOversizedEventSkipped(t *testing.T) {
	for _, format := range []Format{JSON, SSE, NDJSON} {
		t.Run(string(format), func(t *testing.T) {
			p := New(Responses, format)
			prefix, suffix := "", ""
			if format == SSE {
				prefix = "data: "
				suffix = "\n\n"
			}
			if format == NDJSON {
				suffix = "\n"
			}
			p.Feed([]byte(prefix))
			for range 1025 {
				p.Feed(bytes.Repeat([]byte{'x'}, 1024))
			}
			p.Feed([]byte(suffix))
			if len(p.buf) > MaxEventBytes || len(p.event) > MaxEventBytes {
				t.Fatal("unbounded")
			}
			p.FeedEvent([]byte(responseJSON))
			if u := p.Finish(true); u.Source != Unknown {
				t.Fatal(u)
			}
		})
	}
	// Aggregate SSE field bytes, not merely each data line, are bounded.
	p := New(Responses, SSE)
	for range 1100 {
		p.Feed([]byte(": " + strings.Repeat("x", 1024) + "\n"))
	}
	p.Feed([]byte("\n"))
	if !p.bad {
		t.Fatal("oversized non-data fields not detected")
	}
}
func TestModelNameBoundedAndFiltered(t *testing.T) {
	p := New(ChatCompletions, JSON)
	p.FeedEvent([]byte(fmt.Sprintf(`{"model":%q,"usage":{"prompt_tokens":0,"completion_tokens":0}}`, "a\n<script>🙂 "+strings.Repeat("b", 300))))
	u := p.Finish(true)
	if u.Model != "" || u.Input != 0 {
		t.Fatal(u)
	}
}
func TestResponseSnapshotDedup(t *testing.T) {
	p := New(Responses, SSE)
	p.FeedEvent([]byte(responseJSON))
	p.FeedEvent([]byte(responseJSON))
	p.FeedEvent([]byte(strings.ReplaceAll(strings.Replace(responseJSON, `"input_tokens":12`, `"input_tokens":20`, 1), `"r1"`, `"r2"`)))
	u := p.Snapshot()
	if u.Input != 32 || u.Output != 14 || u.Turns != 2 {
		t.Fatal(u)
	}
	// Replacing a previously observed ID updates rather than adds its snapshot.
	p.FeedEvent([]byte(strings.Replace(responseJSON, `"input_tokens":12`, `"input_tokens":1`, 1)))
	if u = p.Finish(true); u.Input != 21 || u.Turns != 2 {
		t.Fatal(u)
	}
	p = New(Responses, SSE)
	for i := range maxResponseIDs + 1 {
		p.FeedEvent([]byte(strings.Replace(responseJSON, `"r1"`, fmt.Sprintf(`"r%d"`, i), 1)))
	}
	if len(p.responses) != maxResponseIDs || p.Finish(true).Source != Unknown {
		t.Fatal(p.Snapshot())
	}
}
func TestUnknownCountsAndOverflow(t *testing.T) {
	for _, value := range []string{`null`, `-1`, `1.5`, `"12"`, `9223372036854775808`} {
		p := New(ChatCompletions, JSON)
		p.FeedEvent([]byte(`{"usage":{"prompt_tokens":` + value + `,"completion_tokens":2}}`))
		if u := p.Finish(true); u.Source != Unknown || u.Input != -1 {
			t.Fatal(value, u)
		}
	}
	p := New(ChatCompletions, JSON)
	p.FeedEvent([]byte(`{"usage":{"prompt_tokens":0}}`))
	if u := p.Finish(true); u.Output != -1 || u.Input != 0 {
		t.Fatal(u)
	}
	if got := sumCount(9223372036854775807, 1); got != -1 {
		t.Fatal(got)
	}
}
func TestUnknownEndpointDoesNotBuffer(t *testing.T) {
	for _, media := range []string{"application/json", "text/event-stream", ""} {
		p := ForResponse("/v1/models", media)
		p.Feed([]byte(responseJSON))
		if cap(p.buf) != 0 || cap(p.event) != 0 || p.Finish(true) != emptyUsage() {
			t.Fatalf("%q: buffered or attributed an unrecognized endpoint", media)
		}
	}
}

func TestStreamScratchReleased(t *testing.T) {
	for _, format := range []Format{SSE, NDJSON} {
		p := New(Responses, format)
		for _, n := range []int{100, 16 << 10} {
			event := `{"type":"response.output_text.delta","delta":"` + strings.Repeat("x", n) + `"}`
			if format == SSE {
				event = "data: " + event + "\n"
			}
			p.Feed([]byte(event + "\n"))
			for _, buf := range [][]byte{p.buf, p.event} {
				if len(buf) != 0 || cap(buf) > 4<<10 {
					t.Fatalf("%s: retained oversized scratch (len=%d cap=%d)", format, len(buf), cap(buf))
				}
				if bytes.Count(buf[:cap(buf)], []byte{0}) != cap(buf) {
					t.Fatalf("%s: retained payload in scratch", format)
				}
			}
		}
	}
}

func TestSelection(t *testing.T) {
	cases := []struct {
		path, ct string
		p        Protocol
		f        Format
	}{
		{"/v1/responses?x=1", "text/event-stream; charset=utf-8", Responses, SSE},
		{"/v1/chat/completions", "application/json", ChatCompletions, JSON},
		{"/v1/messages", "application/json", Messages, JSON},
		{"/models/test:streamGenerateContent", "text/event-stream", Gemini, SSE},
		{"/api/chat", "application/x-ndjson", Ollama, NDJSON},
		{"/other", "application/octet-stream", "", ""},
	}
	for _, tt := range cases {
		p := ForResponse(tt.path, tt.ct)
		if p.protocol != tt.p || p.format != tt.f {
			t.Fatal(tt, p)
		}
	}
}
func FuzzSSE(f *testing.F) {
	f.Add([]byte("data: "+responseJSON+"\n\n"), uint16(17))
	f.Add([]byte("data:\r\r"), uint16(1))
	f.Fuzz(func(t *testing.T, b []byte, chunk uint16) {
		if len(b) > 2*MaxEventBytes {
			t.Skip()
		}
		whole := New(Responses, SSE)
		whole.Feed(b)
		want := whole.Finish(true)
		p := New(Responses, SSE)
		n := int(chunk)%1024 + 1
		for i := 0; i < len(b); i += n {
			p.Feed(b[i:min(i+n, len(b))])
		}
		if got := p.Finish(true); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk mismatch %+v %+v", got, want)
		}
	})
}
func FuzzUsageJSON(f *testing.F) {
	f.Add([]byte(responseJSON))
	f.Add([]byte(chatJSON))
	f.Add([]byte(`{"usage":null}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 2*MaxEventBytes {
			t.Skip()
		}
		for _, proto := range []Protocol{Responses, Messages, ChatCompletions, Gemini, Ollama} {
			p := New(proto, JSON)
			p.FeedEvent(b)
			u := p.Finish(true)
			if len(u.Model) > 128 || u.Input < -1 || u.Output < -1 {
				t.Fatal(u)
			}
		}
	})
}

func TestModelFromEarlierChunk(t *testing.T) {
	for _, tt := range []struct {
		proto       Protocol
		first, last string
	}{
		{ChatCompletions, `{"model":"early"}`, `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`},
		{Gemini, `{"modelVersion":"early"}`, `{"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2}}`},
	} {
		p := New(tt.proto, SSE)
		p.FeedEvent([]byte(tt.first))
		p.FeedEvent([]byte(tt.last))
		if u := p.Finish(true); u.Model != "early" || u.Input != 1 || u.Source != Reported {
			t.Fatal(u)
		}
	}
}
func TestResponseOverflowAndMissingAggregation(t *testing.T) {
	p := New(Responses, SSE)
	p.FeedEvent([]byte(`{"id":"a","usage":{"input_tokens":9223372036854775807,"output_tokens":1}}`))
	p.FeedEvent([]byte(`{"id":"b","usage":{"input_tokens":1,"output_tokens":2}}`))
	if u := p.Finish(true); u.Source != Unknown || u.Input != -1 || u.Output != 3 {
		t.Fatal(u)
	}
	p = New(Responses, SSE)
	p.FeedEvent([]byte(`{"id":"a","model":"one","usage":{"input_tokens":0,"output_tokens":1}}`))
	p.FeedEvent([]byte(`{"id":"b","model":"two","usage":{"output_tokens":2}}`))
	if u := p.Finish(true); u.Input != -1 || u.Output != 3 || u.Model != "" {
		t.Fatal(u)
	}
	p = New(Responses, SSE)
	p.FeedEvent([]byte(fmt.Sprintf(`{"id":%q,"usage":{"input_tokens":1}}`, strings.Repeat("a", 257))))
	if u := p.Finish(true); u.Source != Unknown || u.Turns != 0 {
		t.Fatal(u)
	}
}
func TestEventBoundaryLimits(t *testing.T) {
	// A complete JSON body exactly at the bound is accepted, but not bound+1.
	for _, extra := range []int{0, 1} {
		p := New(ChatCompletions, JSON)
		data := append([]byte(chatJSON), bytes.Repeat([]byte{' '}, MaxEventBytes-len(chatJSON)+extra)...)
		p.Feed(data)
		want := Reported
		if extra > 0 {
			want = Unknown
		}
		if u := p.Finish(true); u.Source != want {
			t.Fatal(extra, u)
		}
	}
	// A huge SSE event is skipped through its true blank delimiter, not through
	// one field line. Even a valid usage event embedded in it must be skipped.
	p := New(Responses, SSE)
	p.Feed([]byte("data: " + strings.Repeat("x", MaxEventBytes) + "\n"))
	p.Feed([]byte("data: " + responseJSON + "\n\n"))
	if p.evidence {
		t.Fatal("parsed data within skipped event")
	}
	p.Feed([]byte("data: " + responseJSON + "\n\n"))
	if !p.evidence || p.Finish(true).Source != Unknown {
		t.Fatal(p.Snapshot())
	}
}

func BenchmarkOpenAIUsageEvent(b *testing.B) {
	for _, tc := range []struct {
		protocol Protocol
		event    string
	}{{ChatCompletions, chatJSON}, {Responses, responseJSON}} {
		b.Run(string(tc.protocol), func(b *testing.B) {
			data := []byte(tc.event)
			b.ReportAllocs()
			for b.Loop() {
				p := New(tc.protocol, JSON)
				p.FeedEvent(data)
				if u := p.Finish(true); u.Source != Reported || u.Total != 19 {
					b.Fatal(u)
				}
			}
		})
	}
}

func TestOpenAIDetailCounterValidity(t *testing.T) {
	for _, tc := range []struct {
		details string
		want    int64
		bad     bool
	}{
		{`{}`, -1, false}, {`{"cached_tokens":0}`, 0, false}, {`{"cached_tokens":null}`, -1, true}, {`{"cached_tokens":"2"}`, -1, true}, {`{"cached_tokens":-1}`, -1, true},
	} {
		o := obj([]byte(`{"usage":{"prompt_tokens":1,"prompt_tokens_details":` + tc.details + `}}`))
		u, bad := readOpenAIUsage(o, false)
		if bad != tc.bad || u.CachedInput != tc.want || u.Input != 1 || u.Output != -1 {
			t.Fatalf("%s: cached=%d input=%d output=%d bad=%v", tc.details, u.CachedInput, u.Input, u.Output, bad)
		}
	}
}
