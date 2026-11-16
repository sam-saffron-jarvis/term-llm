package usagetap

import (
	"bytes"
	"strings"
)

// MaxEventBytes bounds a JSON body, SSE event (including fields), NDJSON line,
// or reassembled WebSocket message. Oversize input makes usage unknown.
const MaxEventBytes = 1 << 20
const maxResponseIDs = 128

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
	eventBytes, lineBytes                  int
	skip, bad, finished, evidence, partial bool
	afterCR                                bool
	usage                                  Usage
	responses                              map[string]Usage
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
	case "application/json":
		format = JSON
	case "text/event-stream":
		format = SSE
	case "application/x-ndjson", "application/ndjson":
		format = NDJSON
	}
	return New(protocol, format)
}

// MarkUnknown records a permanent loss of observability (for example compressed
// frames or unsupported encoding). Previously seen counts are not erased, but
// must not be treated as complete or as a zero bill.
func (p *Parser) MarkUnknown() {
	if !p.finished {
		p.bad = true
	}
}

// Feed observes arbitrary byte chunks. JSON bodies are decoded at Finish;
// SSE and NDJSON events are decoded as soon as their delimiters arrive.
func (p *Parser) Feed(data []byte) {
	if p.finished {
		return
	}
	switch p.format {
	case JSON:
		if p.skip {
			return
		}
		if len(data) > MaxEventBytes-len(p.buf) {
			p.bad = true
			p.skip = true
			p.buf = nil
			return
		}
		p.buf = append(p.buf, data...)
	case SSE, NDJSON:
		for _, b := range data {
			p.feedByte(b)
		}
	}
}

func (p *Parser) feedByte(b byte) {
	if p.afterCR {
		p.afterCR = false
		if b == '\n' {
			return
		}
	}
	if b == '\r' || b == '\n' {
		p.line()
		p.afterCR = b == '\r'
		return
	}
	p.lineBytes = min(p.lineBytes+1, MaxEventBytes+1)
	p.addEventByte()
	if !p.skip {
		p.buf = append(p.buf, b)
	}
}

func (p *Parser) addEventByte() {
	p.eventBytes = min(p.eventBytes+1, MaxEventBytes+1)
	if p.eventBytes > MaxEventBytes {
		p.bad = true
		p.skip = true
		p.buf = nil
		p.event = nil
	}
}

func (p *Parser) line() {
	if p.format == NDJSON {
		if !p.skip && len(bytes.TrimSpace(p.buf)) > 0 {
			p.FeedEvent(p.buf)
		}
		p.resetEvent()
		return
	}
	if p.lineBytes == 0 {
		p.dispatch()
		return
	}
	p.lineBytes = 0
	// Count a normalized line delimiter, including non-data SSE fields.
	p.addEventByte()
	if p.skip {
		return
	}
	line := p.buf
	p.buf = nil
	if bytes.HasPrefix(line, []byte("data:")) {
		value := line[5:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if len(p.event) > 0 {
			p.event = append(p.event, '\n')
		}
		if len(value) > MaxEventBytes-len(p.event) {
			p.bad = true
			p.skip = true
			p.event = nil
			return
		}
		p.event = append(p.event, value...)
	}
}

func (p *Parser) resetEvent() {
	p.buf = nil
	p.event = nil
	p.eventBytes = 0
	p.lineBytes = 0
	p.skip = false
}
func (p *Parser) dispatch() {
	if !p.skip && len(bytes.TrimSpace(p.event)) > 0 {
		p.FeedEvent(p.event)
	}
	p.resetEvent()
}

// Snapshot returns current evidence without closing the observer. Evidence is
// partial until Finish(true); malformed/oversized/unsupported data is unknown.
func (p *Parser) Snapshot() Usage {
	u := p.usage
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
