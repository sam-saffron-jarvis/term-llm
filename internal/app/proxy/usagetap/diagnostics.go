package usagetap

import (
	"bytes"
	"encoding/json/jsontext"
	"slices"
)

// ResponseMetadata is diagnostic evidence, never a pricing input. Reasons and
// statuses are finite wire-family enums. Nil counts are unknown, not zero.
// Choice reasons are indexed and bounded; cumulative events replace, not add.
type ResponseMetadata struct {
	State             string         `json:"state"`
	Status            string         `json:"status,omitempty"`
	Finish            string         `json:"finish,omitempty"`
	IncompleteReason  string         `json:"incomplete_reason,omitempty"`
	Choices           []ChoiceFinish `json:"choices,omitempty"`
	CacheWrite5m      *int64         `json:"cache_write_5m,omitempty"`
	CacheWrite1h      *int64         `json:"cache_write_1h,omitempty"`
	WebSearchRequests *int64         `json:"web_search_requests,omitempty"`
	WebFetchRequests  *int64         `json:"web_fetch_requests,omitempty"`
}

type ChoiceFinish struct {
	Index  int64  `json:"index"`
	Reason string `json:"reason"`
}

type responseDiagnostics struct {
	meta    ResponseMetadata
	invalid bool
}

func (d *responseDiagnostics) snapshot(complete bool) *ResponseMetadata {
	m := d.meta
	if m.Status == "" && m.Finish == "" && m.IncompleteReason == "" && m.CacheWrite5m == nil && m.CacheWrite1h == nil && m.WebSearchRequests == nil && m.WebFetchRequests == nil && !d.invalid {
		return nil
	}
	m.State = "observed"
	if !complete || d.invalid {
		m.State = "partial"
	}
	m.Choices = slices.Clone(m.Choices)
	return &m
}

func diagnosticEnum(o object, key string, allowed ...string) string {
	v := textValue(o[key])
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return ""
}

func (d *responseDiagnostics) enumField(o object, key string, allowed ...string) string {
	value := diagnosticEnum(o, key, allowed...)
	if raw, exists := o[key]; exists && !isJSONNull(raw) && value == "" {
		d.invalid = true
	}
	return value
}

func (d *responseDiagnostics) optionalObject(o object, key string) object {
	child := obj(o[key])
	if raw, exists := o[key]; exists && !isJSONNull(raw) && child == nil {
		d.invalid = true
	}
	return child
}

func (p *Parser) observeDiagnostics(data []byte, o object) {
	// Diagnostics are optional: even malformed/duplicate diagnostic fields must
	// not invalidate usage counters accepted by the existing accounting parser.
	p.diagnosticEventInvalid = !jsontext.Value(data).IsValid()
	if p.diagnosticEventInvalid {
		p.diagnostics.invalid = true
		return
	}
	switch p.protocol {
	case Responses:
		if p.wsStarts == nil {
			p.httpResponseDiagnostics(o)
		} // WS terminals belong only to their turns.
	case ChatCompletions:
		p.diagnostics.choices(o, "choices", "finish_reason", false)
	case Messages:
		p.diagnostics.message(o)
	case Gemini:
		if p.geminiWrapped {
			o = obj(o["response"])
		}
		p.diagnostics.choices(o, "candidates", "finishReason", true)
	case Ollama:
		if bytes.Equal(o["done"], []byte("true")) {
			if finish := p.diagnostics.enumField(o, "done_reason", "stop", "length", "unload"); finish != "" {
				p.diagnostics.meta.Finish = finish
			}
		}
	}
}

func responseDiagnosticsFor(o object, typ string) responseDiagnostics {
	d := responseDiagnostics{}
	d.meta.Status = diagnosticEnum(o, "status", "completed", "incomplete", "failed", "cancelled", "canceled", "in_progress", "queued")
	if raw, exists := o["status"]; exists && !isJSONNull(raw) && d.meta.Status == "" {
		d.invalid = true
	}
	if d.meta.Status == "" {
		switch typ {
		case "response.completed":
			d.meta.Status = "completed"
		case "response.incomplete":
			d.meta.Status = "incomplete"
		case "response.failed":
			d.meta.Status = "failed"
		case "response.cancelled", "response.canceled":
			d.meta.Status = "cancelled"
		}
	}
	if d.meta.Status == "canceled" {
		d.meta.Status = "cancelled"
	}
	details := d.optionalObject(o, "incomplete_details")
	d.meta.IncompleteReason = diagnosticEnum(details, "reason", "max_output_tokens", "content_filter")
	if details != nil {
		if raw, exists := details["reason"]; exists && !isJSONNull(raw) && d.meta.IncompleteReason == "" {
			d.invalid = true
		}
	}
	return d
}

func (d *responseDiagnostics) choices(o object, key, reason string, gemini bool) {
	raw, ok := o[key]
	if !ok || isJSONNull(raw) {
		return
	}
	choices, valid := boundedChoices(raw)
	if !valid {
		d.invalid = true
		d.meta.Choices = nil
		d.meta.Finish = ""
		return
	}
	seen := make(map[int64]bool, len(choices))
	for _, raw := range choices {
		choice := obj(raw)
		if choice == nil {
			d.invalid = true
			continue
		}
		if _, ok := choice[reason]; !ok || isJSONNull(choice[reason]) {
			continue
		}
		value := diagnosticEnum(choice, reason, "stop", "length", "tool_calls", "function_call", "content_filter")
		if gemini {
			value = diagnosticEnum(choice, reason, "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "IMAGE_SAFETY", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "NO_IMAGE", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER")
		}
		index := count(choice, "index")
		// Non-streaming Gemini candidates may omit proto3 index zero. Array
		// zero is omitted even when later candidates explicitly carry index 1.
		if _, exists := choice["index"]; gemini && !exists {
			index = 0
		}
		if value == "" || index < 0 || index > 127 || seen[index] {
			d.invalid = true
			continue
		}
		seen[index] = true
		at := slices.IndexFunc(d.meta.Choices, func(c ChoiceFinish) bool { return c.Index == index })
		if at >= 0 {
			d.meta.Choices[at].Reason = value
		} else if len(d.meta.Choices) < 16 {
			d.meta.Choices = append(d.meta.Choices, ChoiceFinish{Index: index, Reason: value})
		} else {
			d.invalid = true
		}
	}
	slices.SortFunc(d.meta.Choices, func(a, b ChoiceFinish) int { return int(a.Index - b.Index) })
	d.meta.Finish = ""
	for _, c := range d.meta.Choices {
		if d.meta.Finish == "" {
			d.meta.Finish = c.Reason
		} else if d.meta.Finish != c.Reason {
			d.meta.Finish = "mixed"
			break
		}
	}
}

func (d *responseDiagnostics) message(o object) {
	typ := textValue(o["type"])
	switch typ {
	case "message_start":
		o = obj(o["message"])
	case "message_delta":
		delta := obj(o["delta"])
		if v := d.enumField(delta, "stop_reason", "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"); v != "" {
			d.meta.Finish = v
		}
	case "", "message":
	default:
		return
	}
	if v := d.enumField(o, "stop_reason", "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded"); v != "" {
		d.meta.Finish = v
	}
	usage := obj(o["usage"])
	cache := d.optionalObject(usage, "cache_creation")
	tools := d.optionalObject(usage, "server_tool_use")
	d.optionalCount(cache, "ephemeral_5m_input_tokens", &d.meta.CacheWrite5m)
	d.optionalCount(cache, "ephemeral_1h_input_tokens", &d.meta.CacheWrite1h)
	d.optionalCount(tools, "web_search_requests", &d.meta.WebSearchRequests)
	d.optionalCount(tools, "web_fetch_requests", &d.meta.WebFetchRequests)
}

func (d *responseDiagnostics) optionalCount(o object, key string, dst **int64) {
	if _, ok := o[key]; !ok {
		return
	}
	n := count(o, key)
	if n < 0 || n > 1<<53-1 {
		*dst = nil
		d.invalid = true
		return
	}
	*dst = &n
}

// ResponseMetadata returns an independent snapshot, separate from Usage so
// accounting, pricing and legacy snapshot equality remain unchanged.
func (p *Parser) ResponseMetadata() *ResponseMetadata {
	return p.diagnostics.snapshot(p.finished && p.transportComplete && !p.bad)
}
func (e *EventStream) ResponseMetadata() *ResponseMetadata { return e.parser.ResponseMetadata() }
func (w *WebSocket) ResponseMetadata() *ResponseMetadata {
	// A connection is not a turn. Per-turn callbacks own correlated diagnostics.
	if w.parser.wsStarts != nil {
		return nil
	}
	return w.parser.ResponseMetadata()
}

// Avoid allocating a slice proportional to an adversarial candidates array.
// The full event has already been validated; only sixteen bounded entries can
// be retained, irrespective of how many tiny choices a provider sends.
func boundedChoices(raw []byte) ([][]byte, bool) {
	d := jsontext.NewDecoder(bytes.NewBuffer(raw))
	token, err := d.ReadToken()
	if err != nil || token.Kind() != '[' {
		return nil, false
	}
	var choices [][]byte
	for d.PeekKind() != ']' {
		if len(choices) == 16 {
			return nil, false
		}
		value, err := d.ReadValue()
		if err != nil {
			return nil, false
		}
		choices = append(choices, bytes.Clone(value))
	}
	_, err = d.ReadToken()
	return choices, err == nil
}
