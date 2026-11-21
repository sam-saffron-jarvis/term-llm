package usagetap

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

type object map[string]json.RawMessage

func obj(b json.RawMessage) object       { var o object; _ = json.Unmarshal(b, &o); return o }
func textValue(b json.RawMessage) string { var s string; _ = json.Unmarshal(b, &s); return s }
func count(o object, key string) int64 {
	b, ok := o[key]
	if !ok {
		return -1
	}
	var n int64
	if json.Unmarshal(b, &n) != nil || n < 0 || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return -1
	}
	return n
}
func safeModel(s string) string {
	if len(s) > 128 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-", r)) {
			return ""
		}
	}
	return s
}

// responseIdentity preserves exact labels or rejects them. Missing labels are
// absent, never inferred default tiers. Invalid supplied labels forbid pricing.
func responseIdentity(o object, key string, limit int) (string, bool) {
	raw, exists := o[key]
	if !exists {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true
	}
	if len(s) > limit || safeModel(s) != s {
		return "", true
	}
	return s, false
}

// observeModel clears unsafe identities; a conflicting identity in one stream
// is ambiguous rather than silently attributable to the last chunk's model.
func (p *Parser) observeModel(o object, key string) {
	if _, exists := o[key]; !exists {
		return
	}
	model, invalid := responseIdentity(o, key, 128)
	if invalid || (p.usage.Model != "" && model != "" && p.usage.Model != model) {
		p.usage.Ambiguous = true
	}
	p.usage.Model = model
}
func (p *Parser) observeTier(o object) {
	if _, exists := o["service_tier"]; !exists {
		return
	}
	tier, invalid := responseIdentity(o, "service_tier", 64)
	if invalid || (p.usage.Tier != "" && tier != "" && p.usage.Tier != tier) {
		p.usage.Ambiguous = true
	}
	p.usage.Tier = tier
}
func hasCounts(u Usage) bool {
	return u.Input >= 0 || u.Output >= 0 || u.Total >= 0 || u.CachedInput >= 0 || u.CacheWrite >= 0 || u.Reasoning >= 0
}
func malformedCounts(o object, keys ...string) bool {
	for _, k := range keys {
		if _, ok := o[k]; ok && count(o, k) < 0 {
			return true
		}
	}
	return false
}

// FeedEvent observes one complete JSON event (including a decoded WS message).
// It may be used independently of Feed. Do not interleave it with a partially
// buffered Feed event. Non-usage events are ignored; malformed JSON is unknown.
func (p *Parser) FeedEvent(data []byte) { p.feedEvent(data, nil) }

func (p *Parser) feedEvent(data []byte, turns *webSocketTurns) {
	if p.finished || p.protocol == Inapplicable {
		return
	}
	if len(data) > MaxEventBytes {
		p.MarkUnknown()
		return
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("[DONE]")) {
		if p.venice != nil && p.format == SSE {
			p.venice.sawDone = true
		}
		return
	}
	var o object
	if err := json.Unmarshal(data, &o); err != nil || o == nil {
		p.MarkUnknown()
		return
	}
	switch p.protocol {
	case Responses:
		p.response(o, turns)
	case ChatCompletions:
		if p.venice != nil {
			p.venice.observe(data, o["usage"])
		}
		p.chatCompletion(o)
	case Messages:
		p.message(o)
	case Gemini:
		p.gemini(o)
	case Ollama:
		p.ollama(o)
	}
}

func (p *Parser) chatCompletion(o object) {
	// Chat Completions permits a null service_tier when no tier is asserted.
	// Treat it like an omitted field, not a conflicting model/tier identity.
	// This changes only the decoded observer map, never forwarded bytes.
	if bytes.Equal(bytes.TrimSpace(o["service_tier"]), []byte("null")) {
		delete(o, "service_tier")
	}
	p.observeModel(o, "model")
	p.observeTier(o)
	u, bad := readOpenAIUsage(o, false)
	p.bad = p.bad || bad // Invalid counters do not invalidate an independent charge.
	if hasCounts(u) {
		p.set(u)
	}
}

func readOpenAIUsage(o object, responses bool) (u Usage, bad bool) {
	u = emptyUsage()
	u.Model, u.Ambiguous = responseIdentity(o, "model", 128)
	tier, invalidTier := responseIdentity(o, "service_tier", 64)
	u.Tier = tier
	u.Ambiguous = u.Ambiguous || invalidTier
	raw, exists := o["usage"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return u, false
	}
	m := obj(raw)
	if m == nil {
		return u, true
	}
	input, output, inDetails, outDetails := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
	if responses {
		input, output, inDetails, outDetails = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	}
	// An absent counter is unknown. An explicitly malformed one also makes
	// the usage unreliable. Check presence without decoding its value twice.
	read := func(fields object, key string) int64 {
		n := count(fields, key)
		if n < 0 {
			_, exists := fields[key]
			bad = bad || exists
		}
		return n
	}
	u.Input = read(m, input)
	u.Output = read(m, output)
	u.Total = read(m, "total_tokens")
	u.CachedInput = read(obj(m[inDetails]), "cached_tokens")
	u.Reasoning = read(obj(m[outDetails]), "reasoning_tokens")
	return u, bad
}

func (p *Parser) set(u Usage) {
	if u.Model == "" {
		u.Model = p.usage.Model
	}
	if u.Tier == "" {
		u.Tier = p.usage.Tier
	}
	u.Ambiguous = u.Ambiguous || p.usage.Ambiguous
	if u.Ambiguous {
		u.Model = ""
		u.Tier = ""
	}
	u.Turns = 1
	p.usage = u
	p.evidence = true
}

func terminalResponseType(typ string) bool {
	return typ == "response.completed" || typ == "response.incomplete" || typ == "response.failed" || typ == "response.done"
}

func (p *Parser) response(o object, turns *webSocketTurns) {
	typ := textValue(o["type"])
	terminal := terminalResponseType(typ)
	if p.wsStarts != nil && !terminal {
		switch typ {
		case "response.created", "response.in_progress":
			id := textValue(obj(o["response"])["id"])
			p.noteWebSocketStart(id)
			if turns != nil {
				turns.start(id, typ == "response.created")
			}
		case "response.cancelled", "response.canceled":
			p.partial = true
			if turns != nil {
				response := obj(o["response"])
				u, bad := readOpenAIUsage(response, true)
				turns.end(textValue(response["id"]), "incomplete", u, bad)
			}
		case "error":
			p.partial = true
		}
		return
	}
	if typ != "" && !terminal {
		return
	}
	if terminal {
		o = obj(o["response"])
		if o == nil {
			p.MarkUnknown()
			return
		}
	}
	status := textValue(o["status"])
	if typ == "response.incomplete" || typ == "response.failed" || status == "incomplete" || status == "failed" ||
		p.wsStarts != nil && status != "" && status != "completed" {
		p.partial = true
	}
	u, bad := readOpenAIUsage(o, true)
	p.bad = p.bad || bad
	hasUsage := hasCounts(u)
	if !hasUsage && p.wsStarts != nil && terminal {
		p.partial = true
	}
	observeTurn := turns != nil && terminal
	if !hasUsage && !observeTurn {
		return
	}
	id := textValue(o["id"])
	if hasUsage {
		p.recordResponse(id, u)
	}
	if observeTurn {
		turns.end(id, webSocketTurnOutcome(typ, status), u, bad)
	}
}

func (p *Parser) recordResponse(id string, u Usage) {
	// Retain only bounded identifiers, never arbitrary payload strings. A missing
	// ID uses one slot (HTTP snapshots); multi-turn WS accounting requires IDs.
	if len(id) > maxResponseIDBytes {
		p.MarkUnknown()
		return
	}
	if p.responses == nil {
		p.responses = make(map[string]Usage)
	}
	if _, exists := p.responses[id]; !exists && len(p.responses) >= maxResponseIDs {
		p.MarkUnknown()
		return
	}
	p.responses[id] = u
	p.aggregateResponses()
}
func sumCount(a, b int64) int64 {
	if a < 0 || b < 0 {
		return -1
	}
	if a > math.MaxInt64-b {
		return -1
	}
	return a + b
}
func (p *Parser) sumObserved(a, b int64) int64 {
	n := sumCount(a, b)
	if a >= 0 && b >= 0 && n < 0 {
		p.MarkUnknown()
	}
	return n
}
func (p *Parser) aggregateResponses() {
	u := emptyUsage()
	first := true
	for _, v := range p.responses {
		if first {
			u = v
			first = false
		} else {
			u.Input = p.sumObserved(u.Input, v.Input)
			u.CachedInput = p.sumObserved(u.CachedInput, v.CachedInput)
			u.CacheWrite = p.sumObserved(u.CacheWrite, v.CacheWrite)
			u.Output = p.sumObserved(u.Output, v.Output)
			u.Reasoning = p.sumObserved(u.Reasoning, v.Reasoning)
			u.Total = p.sumObserved(u.Total, v.Total)
			if u.Model != v.Model || u.Tier != v.Tier || v.Ambiguous {
				u.Ambiguous = true
			}
		}
	}
	if u.Ambiguous {
		u.Model = ""
		u.Tier = ""
	}
	u.Turns = int64(len(p.responses))
	p.usage = u
	p.evidence = true
}

func (p *Parser) message(o object) {
	typ := textValue(o["type"])
	if typ == "message_start" {
		o = obj(o["message"])
	} else if typ != "" && typ != "message_delta" && typ != "message" {
		return
	}
	p.observeModel(o, "model")
	raw, ok := o["usage"]
	if !ok {
		return
	}
	m := obj(raw)
	if m == nil {
		p.MarkUnknown()
		return
	}
	p.observeTier(m)
	u := p.usage
	// Anthropic delta output_tokens is cumulative, not additive. Cache read/write
	// input is separate from input_tokens; no cache arithmetic is applied.
	pairs := []struct {
		key string
		dst *int64
	}{
		{"input_tokens", &u.Input}, {"cache_read_input_tokens", &u.CachedInput},
		{"cache_creation_input_tokens", &u.CacheWrite}, {"output_tokens", &u.Output},
	}
	for _, v := range pairs {
		if _, ok := m[v.key]; ok {
			*v.dst = count(m, v.key)
			if *v.dst < 0 {
				p.MarkUnknown()
			}
		}
	}
	if hasCounts(u) {
		p.set(u)
	}
}
func (p *Parser) gemini(o object) {
	if p.geminiWrapped {
		o = obj(o["response"])
		if o == nil {
			p.MarkUnknown()
			return
		}
	}
	p.observeModel(o, "modelVersion")
	raw, ok := o["usageMetadata"]
	if !ok {
		return
	}
	m := obj(raw)
	if m == nil {
		p.MarkUnknown()
		return
	}
	u := emptyUsage()
	u.Model = safeModel(textValue(o["modelVersion"]))
	u.Input = count(m, "promptTokenCount")
	u.CachedInput = count(m, "cachedContentTokenCount")
	u.Output = count(m, "candidatesTokenCount")
	u.Reasoning = count(m, "thoughtsTokenCount")
	u.Total = count(m, "totalTokenCount")
	if malformedCounts(m, "promptTokenCount", "cachedContentTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount") {
		p.MarkUnknown()
	}
	// Gemini serializes usageMetadata as proto3 JSON, which omits zero-valued
	// fields. Alongside a reported prompt count, an absent cache-hit or thinking
	// count is an attested zero, not an unobserved component.
	if u.Input >= 0 {
		for _, v := range []struct {
			key string
			dst *int64
		}{{"cachedContentTokenCount", &u.CachedInput}, {"thoughtsTokenCount", &u.Reasoning}} {
			if _, ok := m[v.key]; !ok {
				*v.dst = 0
			}
		}
	}
	if hasCounts(u) {
		p.set(u)
	}
}
func (p *Parser) ollama(o object) {
	if string(o["done"]) != "true" {
		return
	}
	p.observeModel(o, "model")
	u := emptyUsage()
	u.Model = safeModel(textValue(o["model"]))
	u.Input = count(o, "prompt_eval_count")
	u.Output = count(o, "eval_count")
	if malformedCounts(o, "prompt_eval_count", "eval_count") {
		p.MarkUnknown()
	}
	if hasCounts(u) {
		p.set(u)
	}
}
