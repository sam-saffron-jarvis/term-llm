package usagetap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func tierResponse(id, model, tier string) []byte {
	fields := map[string]any{"id": id, "model": model, "usage": map[string]any{"input_tokens": 10, "output_tokens": 3}}
	if tier != "" {
		fields["service_tier"] = tier
	}
	b, _ := json.Marshal(map[string]any{"type": "response.completed", "response": fields})
	return b
}
func TestResponseServiceTierExact(t *testing.T) {
	for _, tier := range []string{"", "default", "auto", "flex", "priority", "unknown-vendor-tier"} {
		p := New(Responses, JSON)
		p.FeedEvent(tierResponse("a", "model", tier))
		u := p.Finish(true)
		if u.Tier != tier || u.Model != "model" || u.Ambiguous || u.Source != Reported {
			t.Fatal(tier, u)
		}
	}
	p := New(Responses, JSON)
	p.Feed([]byte(`{"model":"model","service_tier":"flex","usage":{"input_tokens":2}}`))
	if u := p.Finish(true); u.Tier != "flex" || u.Ambiguous {
		t.Fatal(u)
	}
}
func TestChatServiceTierFromChunk(t *testing.T) {
	p := New(ChatCompletions, SSE)
	p.FeedEvent([]byte(`{"model":"model","service_tier":"flex","choices":[]}`))
	p.FeedEvent([]byte(`{"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	if u := p.Finish(true); u.Tier != "flex" || u.Ambiguous || u.Model != "model" {
		t.Fatal(u)
	}
	for _, label := range []string{`"flex\n"`, `null`, `15`, fmt.Sprintf("%q", strings.Repeat("x", 65))} {
		p = New(ChatCompletions, JSON)
		p.FeedEvent([]byte(`{"model":"model","service_tier":"default"}`))
		p.FeedEvent([]byte(`{"service_tier":` + label + `,"usage":{"prompt_tokens":2}}`))
		if u := p.Finish(true); !u.Ambiguous || u.Tier != "" || u.Model != "" || u.Input != 2 {
			t.Fatal(label, u)
		}
	}
	p = New(ChatCompletions, JSON)
	p.FeedEvent([]byte(`{"model":"model","service_tier":"flex"}`))
	p.FeedEvent([]byte(`{"service_tier":"default","usage":{"prompt_tokens":2}}`))
	if u := p.Finish(true); !u.Ambiguous || u.Tier != "" || u.Model != "" {
		t.Fatal(u)
	}
}
func TestMixedResponsesIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, modelA, modelB, tierA, tierB string
		ambiguous                          bool
	}{
		{"same", "model", "model", "flex", "flex", false},
		{"models", "one", "two", "default", "default", true},
		{"tiers", "model", "model", "flex", "default", true},
		{"unknown tier", "model", "model", "", "default", true},
		{"unknown model", "model", "", "default", "default", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for range 20 { // exercise randomized map iteration order
				p := New(Responses, JSON)
				p.FeedEvent(tierResponse("a", tt.modelA, tt.tierA))
				p.FeedEvent(tierResponse("b", tt.modelB, tt.tierB))
				u := p.Snapshot()
				if u.Ambiguous != tt.ambiguous || u.Turns != 2 || u.Input != 20 {
					t.Fatal(u)
				}
				if tt.ambiguous && (u.Model != "" || u.Tier != "") {
					t.Fatal(u)
				}
				// A replay is not another turn, and an updated authoritative snapshot can
				// resolve ambiguity for the same ID without erasing distinct turns.
				p.FeedEvent(tierResponse("b", tt.modelA, tt.tierA))
				if u = p.Finish(true); u.Ambiguous || u.Model != tt.modelA || u.Tier != tt.tierA || u.Turns != 2 {
					t.Fatal(u)
				}
			}
		})
	}
}

func TestMessagesNestedUsageServiceTierJSONAndSSE(t *testing.T) {
	for _, format := range []Format{JSON, SSE} {
		for _, tier := range []string{"standard", "priority", ""} {
			t.Run(string(format)+"/"+tier, func(t *testing.T) {
				nested := ""
				if tier != "" {
					nested = `,"service_tier":` + fmt.Sprintf("%q", tier)
				}
				message := `{"type":"message","model":"claude-opus-5","service_tier":"ignored-outer","usage":{"input_tokens":1000,"cache_read_input_tokens":100,"cache_creation_input_tokens":0,"output_tokens":200` + nested + `}}`
				p := New(Messages, format)
				if format == SSE {
					p.Feed([]byte("event: message_start\ndata: " + `{"type":"message_start","message":` + message + "}\n\n"))
				} else {
					p.Feed([]byte(message))
				}
				u := p.Finish(true)
				if u.Tier != tier || u.Ambiguous || u.Source != Reported || u.Model != "claude-opus-5" || u.Input != 1000 || u.Output != 200 || u.Turns != 1 {
					t.Fatalf("nested tier not preserved: %+v", u)
				}
			})
		}
	}
}
func TestMessagesNestedTierMalformedAndConflicting(t *testing.T) {
	labels := []string{`null`, `42`, `{}`, `[]`, `"standard\n"`, fmt.Sprintf("%q", strings.Repeat("x", 65)), `"priority"`}
	for _, format := range []Format{JSON, SSE} {
		for _, label := range labels {
			t.Run(string(format)+"/"+label, func(t *testing.T) {
				p := New(Messages, format)
				first := `{"type":"message","model":"claude-opus-5","usage":{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1,"service_tier":"standard"}}`
				delta := `{"type":"message_delta","usage":{"output_tokens":3,"service_tier":` + label + `}}`
				if format == SSE {
					p.Feed([]byte("event: message_start\ndata: " + `{"type":"message_start","message":` + first + "}\n\nevent: message_delta\ndata: " + delta + "\n\n"))
				} else {
					p.FeedEvent([]byte(first))
					p.FeedEvent([]byte(delta))
				}
				u := p.Finish(true)
				if !u.Ambiguous || u.Model != "" || u.Tier != "" || u.Input != 10 || u.Output != 3 {
					t.Fatalf("unsafe/conflicting nested tier was priced as an identity: %+v", u)
				}
			})
		}
	}
}
