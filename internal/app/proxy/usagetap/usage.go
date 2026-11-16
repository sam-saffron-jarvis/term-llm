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
func (p *Parser) FeedEvent(data []byte) {
	if p.finished || p.protocol == Inapplicable {
		return
	}
	if len(data) > MaxEventBytes {
		p.MarkUnknown()
		return
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("[DONE]")) {
		return
	}
	if !json.Valid(data) {
		p.MarkUnknown()
		return
	}
	o := obj(data)
	if o == nil {
		p.MarkUnknown()
		return
	}
	switch p.protocol {
	case Responses:
		p.response(o)
	case ChatCompletions:
		p.openAI(o, false)
	case Messages:
		p.message(o)
	case Gemini:
		p.gemini(o)
	case Ollama:
		p.ollama(o)
	}
}

func (p *Parser) openAI(o object, responses bool) (Usage, bool) {
	u := emptyUsage()
	u.Model, u.Ambiguous = responseIdentity(o, "model", 128)
	tier, invalidTier := responseIdentity(o, "service_tier", 64)
	u.Tier = tier
	u.Ambiguous = u.Ambiguous || invalidTier
	if !responses {
		p.observeModel(o, "model")
		p.observeTier(o)
	}
	raw, exists := o["usage"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return u, false
	}
	m := obj(raw)
	if m == nil {
		p.MarkUnknown()
		return u, false
	}
	input, output, inDetails, outDetails := "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
	if responses {
		input, output, inDetails, outDetails = "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	}
	u.Input = count(m, input)
	u.Output = count(m, output)
	u.Total = count(m, "total_tokens")
	u.CachedInput = count(obj(m[inDetails]), "cached_tokens")
	u.Reasoning = count(obj(m[outDetails]), "reasoning_tokens")
	if malformedCounts(m, input, output, "total_tokens") || malformedCounts(obj(m[inDetails]), "cached_tokens") || malformedCounts(obj(m[outDetails]), "reasoning_tokens") {
		p.MarkUnknown()
	}
	if hasCounts(u) && !responses {
		p.set(u)
	}
	return u, hasCounts(u)
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

func (p *Parser) response(o object) {
	typ := textValue(o["type"])
	terminal := typ == "response.completed" || typ == "response.incomplete" || typ == "response.failed" || typ == "response.done"
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
	if typ == "response.incomplete" || typ == "response.failed" || status == "incomplete" || status == "failed" {
		p.partial = true
	}
	u, ok := p.openAI(o, true)
	if !ok {
		return
	}
	id := textValue(o["id"])
	// Retain only bounded identifiers, never arbitrary payload strings. A missing
	// ID uses one slot (HTTP snapshots); multi-turn WS accounting requires IDs.
	if len(id) > 256 {
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
