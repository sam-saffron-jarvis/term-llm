package usagetap

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestResponseDiagnosticsFamilies(t *testing.T) {
	tests := []struct {
		name                 string
		protocol             Protocol
		format               Format
		data, finish, status string
	}{
		{"responses", Responses, JSON, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":1,"output_tokens":2}}`, "", "incomplete"},
		{"terminal without usage", Responses, SSE, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", "", "completed"},
		{"chat mixed", ChatCompletions, JSON, `{"choices":[{"index":0,"finish_reason":"stop"},{"index":1,"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`, "mixed", ""},
		{"gemini proto zero index", Gemini, JSON, `{"candidates":[{"finishReason":"STOP"},{"index":1,"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4}}`, "mixed", ""},
		{"messages", Messages, SSE, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"pause_turn\"},\"usage\":{\"output_tokens\":4}}\n\n", "pause_turn", ""},
		{"ollama", Ollama, NDJSON, "{\"done\":true,\"done_reason\":\"length\",\"prompt_eval_count\":3,\"eval_count\":4}\n", "length", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for split := 0; split <= len(tt.data); split++ {
				p := New(tt.protocol, tt.format)
				p.Feed([]byte(tt.data[:split]))
				p.Feed([]byte(tt.data[split:]))
				p.Finish(true)
				m := p.ResponseMetadata()
				if m == nil || m.Finish != tt.finish || m.Status != tt.status {
					t.Fatalf("split %d %+v", split, m)
				}
				// Provider termination and transport completeness are independent.
				if tt.status == "incomplete" && m.State != "observed" {
					t.Fatal(m)
				}
				if tt.finish == "mixed" && (len(m.Choices) != 2 || m.Choices[0].Index != 0 || m.Choices[1].Index != 1) {
					t.Fatal(m)
				}
			}
		})
	}
}

func TestResponseDiagnosticsReplayPartialAndPrivacy(t *testing.T) {
	p := New(ChatCompletions, SSE)
	data := `{"choices":[{"index":0,"finish_reason":"length","message":{"content":"PRIVATE_PROMPT"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`
	p.FeedEvent([]byte(data))
	p.FeedEvent([]byte(data))
	u := p.Finish(false)
	m := p.ResponseMetadata()
	if u.Output != 4 || m.State != "partial" || len(m.Choices) != 1 {
		t.Fatal(u, m)
	}
	m.Choices[0].Reason = "mutated"
	if p.ResponseMetadata().Choices[0].Reason != "length" {
		t.Fatal("snapshot aliases parser state")
	}
	raw, _ := json.Marshal(p.ResponseMetadata())
	if bytes.Contains(raw, []byte("PRIVATE_")) {
		t.Fatal(string(raw))
	}
	for _, bad := range []string{
		`{"choices":[{"index":0,"finish_reason":"PRIVATE_REASON"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		`{"choices":[{"index":0,"finish_reason":"stop"},{"index":0,"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		`{"choices":[{"index":999,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
		`{"choices":[{"index":0,"finish_reason":"stop","finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
	} {
		p := New(ChatCompletions, JSON)
		p.Feed([]byte(bad))
		u := p.Finish(true)
		m := p.ResponseMetadata()
		if u.Source != Reported || u.Input != 3 || u.Output != 4 {
			t.Fatal("optional diagnostics poisoned usage", u)
		}
		if m != nil && m.State != "partial" {
			t.Fatal(m)
		}
		raw, _ := json.Marshal(m)
		if bytes.Contains(raw, []byte("PRIVATE_")) {
			t.Fatal(string(raw))
		}
	}
	p = New(Responses, JSON)
	p.Feed([]byte(`{"status":"PRIVATE_STATUS","usage":{"input_tokens":3,"output_tokens":4}}`))
	u = p.Finish(true)
	if u.Source != Reported || p.ResponseMetadata().State != "partial" {
		t.Fatal(u, p.ResponseMetadata())
	}
}

func TestAnthropicDiagnosticCounters(t *testing.T) {
	data := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":6,"ephemeral_1h_input_tokens":4},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":2}}}`
	p := New(Messages, SSE)
	p.FeedEvent([]byte(data))
	p.FeedEvent([]byte(data))
	u := p.Finish(true)
	m := p.ResponseMetadata()
	if u.CacheWrite != 10 || u.Input != 3 || u.Output != 4 || *m.CacheWrite5m != 6 || *m.CacheWrite1h != 4 || *m.WebSearchRequests != 0 || *m.WebFetchRequests != 2 {
		t.Fatal(u, m)
	}
	p = New(Messages, JSON)
	p.Feed([]byte(strings.Replace(data, `"ephemeral_5m_input_tokens":6`, `"ephemeral_5m_input_tokens":"PRIVATE_BAD"`, 1)))
	u = p.Finish(true)
	m = p.ResponseMetadata()
	if u.Source != Reported || u.CacheWrite != 10 || m.CacheWrite5m != nil || *m.CacheWrite1h != 4 || m.State != "partial" {
		t.Fatal(u, m)
	}
	// Exercise repository-captured wire evidence rather than inventing fields.
	for _, fixture := range []string{"tool_adaptive/turn1.response", "search/turn1.response", "search_tools_adaptive/turn1.response"} {
		raw, err := os.ReadFile("../../../llm/testdata/anthropic_golden/" + fixture)
		if err != nil {
			t.Fatal(err)
		}

		p := New(Messages, SSE)
		p.Feed(raw)
		p.Finish(true)
		if p.ResponseMetadata() == nil {
			t.Fatalf("no metadata for %s: %s", fixture, raw[:min(100, len(raw))])
		}
	}
}

func TestResponseDiagnosticsChoiceBound(t *testing.T) {
	p := New(ChatCompletions, JSON)
	p.Feed([]byte(`{"choices":[` + strings.TrimSuffix(strings.Repeat(`{"index":0,"finish_reason":"stop"},`, 17), ",") + `],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	if u := p.Finish(true); u.Source != Reported {
		t.Fatal(u)
	}
	if m := p.ResponseMetadata(); m == nil || len(m.Choices) != 0 || m.State != "partial" {
		t.Fatal(m)
	}
}

func TestWebSocketDiagnosticFirstTerminalPerTurn(t *testing.T) {
	w := NewWebSocket(New(Responses, JSON))
	var turns []WebSocketTurn
	w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
	feedTurnEvent(w, `{"type":"response.completed","response":{"id":"PRIVATE_RESPONSE_ID","status":"completed"}}`)
	feedTurnEvent(w, `{"type":"response.incomplete","response":{"id":"PRIVATE_RESPONSE_ID","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":100}}}`)
	feedTurnEvent(w, `{"type":"response.completed","response":{"id":"OTHER_PRIVATE_ID","status":"completed","usage":{"input_tokens":3,"output_tokens":4}}}`)
	w.Finish(true)
	if len(turns) != 2 || turns[0].Usage.Source != Unknown || turns[0].ResponseMeta.Status != "completed" || turns[0].ResponseMeta.IncompleteReason != "" || turns[1].Usage.Input != 3 || turns[1].ResponseMeta.Status != "completed" {
		t.Fatal(turns)
	}
	for _, turn := range turns {
		raw, _ := json.Marshal(turn.ResponseMeta)
		if bytes.Contains(raw, []byte("PRIVATE")) {
			t.Fatal(string(raw))
		}
	}
}

func TestInvalidOptionalFinishDoesNotInventSuccess(t *testing.T) {
	p := New(Messages, SSE)
	p.FeedEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`))
	p.FeedEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"PRIVATE_REASON"},"usage":{"output_tokens":5,"cache_creation":"PRIVATE_BAD_OBJECT"}}`))
	if u := p.Finish(true); u.Source != Reported || u.Output != 5 {
		t.Fatal(u)
	}
	m := p.ResponseMetadata()
	if m == nil || m.State != "partial" {
		t.Fatal(m)
	}
	raw, _ := json.Marshal(m)
	if bytes.Contains(raw, []byte("PRIVATE_")) {
		t.Fatal(string(raw))
	}
}

func TestHTTPResponseIDsAreNotAnUnqualifiedLastFinish(t *testing.T) {
	p := New(Responses, SSE)
	p.FeedEvent([]byte(`{"type":"response.incomplete","response":{"id":"one","status":"incomplete","usage":{"input_tokens":3,"output_tokens":4}}}`))
	p.FeedEvent([]byte(`{"type":"response.completed","response":{"id":"two","status":"completed","usage":{"input_tokens":5,"output_tokens":6}}}`))
	p.FeedEvent([]byte(`{"type":"response.completed","response":{"id":"two","status":"completed","usage":{"input_tokens":5,"output_tokens":6}}}`))
	if u := p.Finish(true); u.Input != 8 || u.Output != 10 || u.Turns != 2 {
		t.Fatal(u)
	}
	if m := p.ResponseMetadata(); m == nil || m.State != "partial" {
		t.Fatal(m)
	}
}
