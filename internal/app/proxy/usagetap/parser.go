package usagetap

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"strings"
)

// MaxEventBytes bounds a JSON body, SSE event (including fields), NDJSON line,
// or reassembled WebSocket message. Oversize input makes usage unknown.
const MaxEventBytes = 1 << 20
const (
	maxResponseIDs     = 128
	maxResponseIDBytes = 256
)

// UnknownTokens distinguishes an absent count from a provider-reported zero.
const UnknownTokens int64 = -1

// Source describes evidence, not billing certainty.
type Source string

const (
	Reported      Source = "reported"
	Partial       Source = "partial"
	Unknown       Source = "unknown"
	NotApplicable Source = "not_applicable"
)

// Usage is a value snapshot. Missing counts are UnknownTokens, not zero.
// Input includes cache reads for OpenAI/Gemini, but excludes cache reads and
// writes for Anthropic. Reasoning is a subset of Output for OpenAI, but separate
// from candidates Output for Gemini. No total is synthesized across conventions.
type Usage struct {
	Input       int64  `json:"input"`
	CachedInput int64  `json:"cached_input"`
	CacheWrite  int64  `json:"cache_write"`
	Output      int64  `json:"output"`
	Reasoning   int64  `json:"reasoning"`
	Total       int64  `json:"total"`
	Model       string `json:"model"`
	Tier        string `json:"tier"`
	Ambiguous   bool   `json:"ambiguous"`
	Source      Source `json:"source"`
	Turns       int64  `json:"turns"`
	// VeniceCostMicros is a verified provider-reported charge in USD micros.
	// Nil means absent or invalid; a non-nil zero is an actual observation.
	VeniceCostMicros *int64 `json:"venice_cost_micros,omitempty"`
}

// Protocol identifies the provider's response schema, never request content.
type Protocol string

const (
	Responses       Protocol = "responses"
	ChatCompletions Protocol = "chat_completions"
	Messages        Protocol = "messages"
	Gemini          Protocol = "gemini"
	Ollama          Protocol = "ollama"
	Bedrock         Protocol = "bedrock" // Use NewEventStream for bounded Anthropic eventstream decoding.
	Inapplicable    Protocol = "not_applicable"
)

// Format identifies already-decoded HTTP entity bytes. Content encodings must
// be handled by the caller, under its own decompression limits.
type Format string

const (
	JSON   Format = "json"
	SSE    Format = "sse"
	NDJSON Format = "ndjson"
)

// Parser is an incremental, single-owner observer. It never mutates input.
// Do not call its methods concurrently. Use a new instance per HTTP response
// or WS connection. FeedEvent can instead observe decoded JSON WS messages.
type Parser struct {
	protocol                               Protocol
	format                                 Format
	buf, event                             []byte
	eventBytes                             int
	lineNonEmpty                           bool // A skipped oversize line is still not an SSE event delimiter.
	skip, bad, finished, evidence, partial bool
	afterCR                                bool
	geminiWrapped                          bool // Cloud Code wraps each Gemini reply in a response field.
	usage                                  Usage
	responses                              map[string]Usage
	wsStarts                               map[string]struct{}
	venice                                 *veniceCharge
}

func emptyUsage() Usage {
	return Usage{Input: -1, CachedInput: -1, CacheWrite: -1, Output: -1, Reasoning: -1, Total: -1, Source: Unknown}
}

// Protocol returns the schema selected for this observer.
func (p *Parser) Protocol() Protocol { return p.protocol }

// New constructs an observer. Unsupported protocols/formats remain unknown.
func New(protocol Protocol, format Format) *Parser {
	return &Parser{protocol: protocol, format: format, usage: emptyUsage()}
}

// ForResponse selects the common response schemas by relative request path
// and content type. It does not infer applicability for media/account endpoints.
// Callers with explicit endpoint policy can use New(Inapplicable, JSON).
func ForResponse(path, contentType string) *Parser {
	path, _, _ = strings.Cut(path, "?")
	var protocol Protocol
	switch {
	case strings.HasSuffix(path, "/responses"):
		protocol = Responses
	case strings.HasSuffix(path, "/chat/completions"):
		protocol = ChatCompletions
	case strings.HasSuffix(path, "/messages"):
		protocol = Messages
	case strings.HasSuffix(path, ":generateContent"), strings.HasSuffix(path, ":streamGenerateContent"):
		protocol = Gemini
	case strings.HasSuffix(path, "/api/chat"), strings.HasSuffix(path, "/api/generate"):
		protocol = Ollama
	}
	media, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	var format Format
	switch strings.TrimSpace(media) {
	case "":
		format = missingContentType
	case "application/json":
		format = JSON
	case "text/event-stream":
		format = SSE
	case "application/x-ndjson", "application/ndjson":
		format = NDJSON
	}
	p := New(protocol, format)
	p.geminiWrapped = path == "/v1internal:streamGenerateContent" || path == "/v1internal:generateContent"
	return p
}

// MarkUnknown records a permanent loss of observability (for example compressed
// frames or unsupported encoding). Previously seen counts are not erased, but
// must not be treated as complete or as a zero bill.
func (p *Parser) MarkUnknown() {
	if !p.finished {
		p.bad = true
		if p.venice != nil {
			p.venice.bad = true
		}
	}
}

// Feed observes arbitrary byte chunks. JSON bodies are decoded at Finish;
// SSE and NDJSON events are decoded as soon as their delimiters arrive.
func (p *Parser) Feed(data []byte) {
	// Without an endpoint schema no body can supply usage evidence.
	if p.finished || p.protocol == "" {
		return
	}
	switch p.format {
	case missingContentType:
		p.detectFraming(data)
	case JSON:
		if p.skip {
			return
		}
		if len(data) > MaxEventBytes-len(p.buf) {
			p.MarkUnknown()
			p.skip = true
			p.buf = nil
			return
		}
		p.buf = append(p.buf, data...)
	case SSE, NDJSON:
		p.feedLines(data)
	}
}

func (p *Parser) feedLines(data []byte) {
	for len(data) > 0 {
		if p.afterCR {
			p.afterCR = false
			if data[0] == '\n' {
				data = data[1:]
				continue
			}
		}
		n := 0
		for n < len(data) && data[n] != '\r' && data[n] != '\n' {
			n++
		}
		if n != 0 {
			p.lineNonEmpty = true
			p.addEventBytes(n)
			if !p.skip {
				p.buf = append(p.buf, data[:n]...)
			}
		}
		if n == len(data) {
			return
		}
		p.line()
		p.afterCR = data[n] == '\r'
		data = data[n+1:]
	}
}

func (p *Parser) addEventBytes(n int) {
	if p.skip {
		return
	}
	if n > MaxEventBytes-p.eventBytes {
		p.MarkUnknown()
		p.skip = true
		p.buf = nil
		p.event = nil
		return
	}
	p.eventBytes += n
}

func (p *Parser) line() {
	if p.format == NDJSON {
		if !p.skip && len(bytes.TrimSpace(p.buf)) > 0 {
			p.FeedEvent(p.buf)
		}
		p.resetEvent()
		return
	}
	if !p.lineNonEmpty {
		p.dispatch()
		return
	}
	p.lineNonEmpty = false
	// Count a normalized line delimiter, including non-data SSE fields.
	p.addEventBytes(1)
	if p.skip {
		return
	}
	line := p.buf
	if bytes.HasPrefix(line, []byte("data:")) {
		value := line[5:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if len(p.event) > 0 {
			p.event = append(p.event, '\n')
		}
		if len(value) > MaxEventBytes-len(p.event) {
			p.MarkUnknown()
			p.skip = true
			p.buf = nil
			p.event = nil
			return
		}
		p.event = append(p.event, value...)
	}
	p.buf = resetScratch(p.buf)
}

// Reuse small buffers only within this parser, without retaining old payload
// bytes or pinning a large event for the remaining lifetime of the stream.
func resetScratch(b []byte) []byte {
	if cap(b) > 4<<10 {
		return nil
	}
	clear(b)
	return b[:0]
}

func (p *Parser) resetEvent() {
	p.buf = resetScratch(p.buf)
	p.event = resetScratch(p.event)
	p.eventBytes = 0
	p.lineNonEmpty = false
	p.skip = false
}

func (p *Parser) dispatch() {
	data := bytes.TrimSpace(p.event)
	if !p.skip && len(data) > 0 {
		if p.protocol != Responses || p.wsStarts != nil || !ordinaryResponseEvent(data) {
			p.FeedEvent(p.event)
		}
	}
	p.resetEvent()
}

// Only HTTP Responses ignores every non-empty, nonterminal type. Decode just
// the discriminator, not e.g. the delta text or tool arguments. v2 validates
// the entire input and matches case-sensitively; duplicate names and any other
// decode failures go through the original decoder with its compatibility rules.
func ordinaryResponseEvent(data []byte) bool {
	var event struct {
		Type string `json:"type"`
	}
	return jsonv2.Unmarshal(data, &event) == nil && event.Type != "" && !terminalResponseType(event.Type)
}

// Snapshot returns current evidence without closing the observer. Evidence is
// partial until Finish(true); malformed/oversized/unsupported data is unknown.
func (p *Parser) Snapshot() Usage {
	u := p.usage
	if v := p.venice; v != nil && p.finished && !p.partial && (p.format == JSON || p.format == SSE) && v.present && !v.bad {
		micros := v.micros
		u.VeniceCostMicros = &micros
	}
	if u.Ambiguous {
		u.Model = ""
		u.Tier = ""
	}
	switch {
	case p.protocol == Inapplicable:
		u.Source = NotApplicable
	case p.bad || !p.evidence:
		u.Source = Unknown
	case !p.finished || p.partial:
		u.Source = Partial
	default:
		u.Source = Reported
	}
	return u
}

// Finish closes the observer and returns a stable snapshot. complete is the
// caller's transport-completion observation, not a claim of provider success.
// Incomplete buffered JSON/lines are discarded; prior evidence stays partial.
// Repeated calls return the original result; subsequent feeds are ignored.
func (p *Parser) Finish(complete bool) Usage {
	if p.finished {
		return p.Snapshot()
	}
	if complete {
		switch p.format {
		case JSON:
			if !p.skip && len(bytes.TrimSpace(p.buf)) > 0 {
				p.FeedEvent(p.buf)
			}
		case NDJSON:
			if len(p.buf) > 0 || p.skip {
				p.line()
			}
		case SSE:
			if len(p.buf) > 0 {
				p.line()
			}
			p.dispatch()
		}
	} else {
		p.partial = true
	}
	p.buf = nil
	p.event = nil
	p.finished = true
	return p.Snapshot()
}
