package usagetap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReviewHTTPDiagnosticUncertaintyIsMonotonic(t *testing.T) {
	terminal := func(id string, usage bool) string {
		counts := ""
		if usage {
			counts = `,"usage":{"input_tokens":3,"output_tokens":4}`
		}
		return fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"status":"completed"%s}}`, id, counts)
	}
	for _, tt := range []struct {
		name   string
		events []string
		state  string
		input  int64
	}{
		{"optional duplicate then valid", []string{`{"type":"response.created","optional":1,"optional":2}`, terminal("one", true)}, "partial", 3},
		{"two usage free IDs", []string{terminal("one", false), terminal("two", false)}, "partial", -1},
		{"two usage IDs then usage free", []string{terminal("one", true), terminal("two", true), terminal("three", false)}, "partial", 6},
		{"two mixed IDs then usage free", []string{terminal("one", false), terminal("two", true), terminal("two", false)}, "partial", 3},
		{"duplicate replay", []string{terminal("one", true), terminal("one", false), terminal("one", true)}, "observed", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Responses, SSE)
			for _, event := range tt.events {
				p.Feed([]byte("data: " + event + "\n\n"))
			}
			u := p.Finish(true)
			m := p.ResponseMetadata()
			if m == nil || m.State != tt.state || u.Input != tt.input {
				t.Fatal(u, m)
			}
			if tt.input >= 0 && u.Source != Reported {
				t.Fatal("diagnostics poisoned usage", u)
			}
		})
	}
}

func TestReviewHTTPCancellationDiagnosticsPreserveAccounting(t *testing.T) {
	for _, spelling := range []string{"cancelled", "canceled"} {
		p := New(Responses, SSE)
		p.Feed([]byte("data: " + responseJSON + "\n\n"))
		before := p.Snapshot()
		p.Feed([]byte(fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"r1\",\"status\":\"%s\",\"usage\":{\"input_tokens\":999}}}\n\n", spelling, spelling)))
		after := p.Finish(true)
		m := p.ResponseMetadata()
		if after.Input != before.Input || after.Output != before.Output || after.Source != Reported || m == nil || m.Status != "cancelled" || m.State != "partial" {
			t.Fatal(after, m)
		}
		only := New(Responses, SSE)
		only.Feed([]byte(fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"only\"}}\n\n", spelling)))
		if u := only.Finish(true); u.Source != Unknown {
			t.Fatal(u)
		}
		if m := only.ResponseMetadata(); m == nil || m.Status != "cancelled" || m.State != "observed" {
			t.Fatal(m)
		}
	}
}

func TestReviewMalformedIncompleteDetailsAndIDBounds(t *testing.T) {
	for _, details := range []string{`"PRIVATE"`, `[]`, `42`, `{"reason":"PRIVATE"}`} {
		p := New(Responses, JSON)
		p.Feed([]byte(`{"id":"one","status":"incomplete","incomplete_details":` + details + `,"usage":{"input_tokens":3,"output_tokens":4}}`))
		if u := p.Finish(true); u.Input != 3 || u.Source != Partial {
			t.Fatal(u)
		}
		m := p.ResponseMetadata()
		if m == nil || m.State != "partial" {
			t.Fatal(m)
		}
		raw, _ := json.Marshal(m)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal(string(raw))
		}
	}
	p := New(Responses, SSE)
	for i := 0; i < maxResponseIDs+10; i++ {
		p.FeedEvent([]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"id-%d"}}`, i)))
	}
	if len(p.diagnosticResponseIDs) != maxResponseIDs {
		t.Fatal("unbounded identities", len(p.diagnosticResponseIDs))
	}
	p.Finish(true)
	if m := p.ResponseMetadata(); m == nil || m.State != "partial" {
		t.Fatal(m)
	}
	for _, id := range []string{strings.Repeat("x", maxResponseIDBytes+1), ""} {
		p := New(Responses, JSON)
		p.Feed([]byte(fmt.Sprintf(`{"id":%q,"status":"completed","usage":{"input_tokens":3}}`, id)))
		u := p.Finish(true)
		if (id == "" && u.Input != 3) || (id != "" && u.Source != Unknown) || p.ResponseMetadata().State != "partial" {
			t.Fatal(u, p.ResponseMetadata())
		}
	}
}

func TestReviewWSConnectionNeverClaimsTurnFinish(t *testing.T) {
	for _, second := range []string{"completed", "incomplete", "failed", "cancelled", "canceled"} {
		w := NewWebSocket(New(Responses, JSON))
		var turns []WebSocketTurn
		w.SetTurnObserver(func(turn WebSocketTurn) { turns = append(turns, turn) })
		feedTurnEvent(w, `{"type":"response.created","response":{"id":"r1"}}`)
		feedTurnEvent(w, responseJSON)
		feedTurnEvent(w, `{"type":"response.created","response":{"id":"r2"}}`)
		event := fmt.Sprintf(`{"type":"response.%s","response":{"id":"r2","status":"%s","usage":{"input_tokens":3,"output_tokens":4}}}`, second, second)
		feedTurnEvent(w, event)
		feedTurnEvent(w, `{"type":"response.completed","response":{"id":"r2","status":"completed","usage":{"input_tokens":999}}}`)
		usage := w.Finish(true)
		if w.ResponseMetadata() != nil || len(turns) != 2 || turns[0].ResponseMeta.Status != "completed" || turns[1].Usage.Input != 3 {
			t.Fatal(usage, turns, w.ResponseMetadata())
		}
		want := second
		if want == "canceled" {
			want = "cancelled"
		}
		if turns[1].ResponseMeta.Status != want {
			t.Fatal(turns[1])
		}
	}
}

func TestReviewOllamaReplayAndOptionalNullCounters(t *testing.T) {
	p := New(Ollama, NDJSON)
	p.FeedEvent([]byte(`{"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":4}`))
	p.FeedEvent([]byte(`{"done":true,"prompt_eval_count":3,"eval_count":4}`))
	if u := p.Finish(true); u.Input != 3 || u.Output != 4 || p.ResponseMetadata().Finish != "stop" {
		t.Fatal(u, p.ResponseMetadata())
	}
	p = New(Messages, SSE)
	p.FeedEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4,"cache_creation":{"ephemeral_1h_input_tokens":8},"server_tool_use":{"web_search_requests":2}}}`))
	p.FeedEvent([]byte(`{"type":"message_delta","usage":{"output_tokens":5,"cache_creation":{"ephemeral_1h_input_tokens":null},"server_tool_use":{"web_search_requests":null}}}`))
	u := p.Finish(true)
	m := p.ResponseMetadata()
	if u.Source != Reported || u.Output != 5 || m.CacheWrite1h != nil || m.WebSearchRequests != nil || m.State != "partial" {
		t.Fatal(u, m)
	}
}
