// Package requestmeta passively observes bounded, allowlisted HTTP inference settings.
package requestmeta

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// MaxBytes bounds transient request JSON; larger bodies are forwarded unobserved.
const MaxBytes = 1 << 20

// Metadata contains only typed settings, never payloads, tool identities or IDs.
// Missing settings mean not sent only when State is observed. Invalid settings
// leave State invalid, while independently valid settings may remain available.
type Metadata struct {
	State              string   `json:"state"`
	Protocol           string   `json:"protocol,omitempty"`
	Model              string   `json:"model,omitempty"`
	ReasoningEffort    string   `json:"reasoning_effort,omitempty"`
	ReasoningMode      string   `json:"reasoning_mode,omitempty"`
	ReasoningContext   string   `json:"reasoning_context,omitempty"`
	ReasoningSummary   string   `json:"reasoning_summary,omitempty"`
	ThinkingMode       string   `json:"thinking_mode,omitempty"`
	ThinkingBudget     *int64   `json:"thinking_budget,omitempty"`
	ThinkingBudgetMode string   `json:"thinking_budget_mode,omitempty"`
	OutputLimit        *int64   `json:"output_limit,omitempty"`
	OutputLimitMode    string   `json:"output_limit_mode,omitempty"`
	ServiceTier        string   `json:"service_tier,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"`
	TopP               *float64 `json:"top_p,omitempty"`
	Stream             *bool    `json:"stream,omitempty"`
	Verbosity          string   `json:"verbosity,omitempty"`
	ToolChoice         string   `json:"tool_choice,omitempty"`
	ParallelTools      *bool    `json:"parallel_tools,omitempty"`
	DeclaredTools      *int64   `json:"declared_tools,omitempty"`
	MultiAgent         *bool    `json:"multi_agent,omitempty"`
	AgentLimit         *int64   `json:"agent_limit,omitempty"`
	CacheMode          string   `json:"cache_mode,omitempty"`
	CacheTTL           string   `json:"cache_ttl,omitempty"`
}

// Observer does not read ahead or publish from transport callbacks. The dispatch
// owner calls Finalize before persisting its record. Late reads are ignored.
// Close preserves the underlying result and may run concurrently with Read.
type Observer struct {
	body                           io.ReadCloser
	ctx                            context.Context
	mu                             sync.Mutex
	once                           sync.Once
	finalOnce                      sync.Once
	closeErr                       error
	buf                            []byte
	seen, length                   int64
	eof, failed, closed, finalized bool
	meta                           Metadata
	path, profile, upstream        string
}

// New uses the actual outgoing path and endpoint-relative path (relative to the
// configured upstream root), not the provider's display name. endpoint permits
// custom API roots without treating arbitrary suffix matches as inference.
func New(body io.ReadCloser, ctx context.Context, method, path, endpoint, profile, upstream string, headers http.Header, length int64) *Observer {
	protocol := protocolFor(method, path, endpoint)
	o := &Observer{body: body, ctx: ctx, length: length, path: path, profile: profile, upstream: upstream, meta: Metadata{State: "not_observed", Protocol: protocol}}
	ct, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	enc := strings.TrimSpace(strings.Join(headers.Values("Content-Encoding"), ","))
	switch {
	case protocol == "" || headers.Get("Upgrade") != "":
		o.meta.State = "unsupported"
	case len(headers.Values("Content-Type")) > 1 || err != nil || ct != "application/json" || enc != "" && !strings.EqualFold(enc, "identity"):
		o.meta.State = "unsupported"
	case length > MaxBytes:
		o.meta.State = "truncated"
	}
	return o
}

func (o *Observer) Read(p []byte) (int, error) {
	n, err := o.body.Read(p)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.finalized {
		return n, err
	}
	o.seen += int64(n)
	if o.meta.State == "not_observed" && n > 0 {
		if n > MaxBytes-len(o.buf) {
			o.meta.State = "truncated"
			o.buf = nil
		} else {
			if o.buf == nil && o.length > 0 {
				o.buf = make([]byte, 0, int(o.length))
			}
			if len(o.buf)+n > cap(o.buf) {
				capacity := min(MaxBytes, max(len(o.buf)+n, max(4096, cap(o.buf)*2)))
				grown := make([]byte, len(o.buf), capacity)
				copy(grown, o.buf)
				o.buf = grown
			}
			o.buf = append(o.buf, p[:n]...)
		}
	}
	if err == io.EOF {
		o.eof = true
	} else if err != nil {
		o.failed = true
	}
	return n, err
}

func (o *Observer) Close() error {
	o.once.Do(func() {
		o.closeErr = o.body.Close()
		o.mu.Lock()
		o.closed = true
		// HTTP/1.1 closes the body before flushing its tail and trailers. Never
		// parse here: freeze reads and release only unusable observation bytes.
		if o.meta.State != "not_observed" || o.failed || !o.complete() {
			o.buf = nil
		}
		o.mu.Unlock()
	})
	return o.closeErr
}

// complete is called only under mu. Known-length transports need not read EOF.
func (o *Observer) complete() bool {
	return o.eof && o.length < 0 || o.length >= 0 && o.seen == o.length
}

// Finalize runs once in the dispatch owner before terminal persistence. It
// detaches the bounded bytes before parsing so Read/Close never wait on parsing.
// An in-flight read losing the freeze race conservatively leaves settings partial.
func (o *Observer) Finalize() Metadata {
	o.finalOnce.Do(o.finalize)
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.meta
}

func (o *Observer) finalize() {
	o.mu.Lock()
	o.finalized = true
	meta, data := o.meta, o.buf
	complete, failed, seen := o.complete(), o.failed, o.seen
	o.buf = nil
	o.mu.Unlock()
	defer func() {
		// Optional observation must not interrupt dispatch/persistence even if
		// a decoder unexpectedly panics. No raw value escapes this boundary.
		if recover() != nil {
			meta = Metadata{State: "invalid", Protocol: meta.Protocol}
		}
		o.mu.Lock()
		o.meta = meta
		o.mu.Unlock()
	}()
	if meta.State != "not_observed" {
		return
	}
	// Cancellation after full consumption belongs to the response lifecycle.
	// It must not erase an already completely observed request.
	if failed || !complete {
		if seen > 0 {
			meta.State = "truncated"
		}
		return
	}
	meta = parse(data, meta.Protocol, o.path, o.profile, o.upstream)
}

// ForRequest uses the rewritten upstream root while leaving request trailers,
// non-replayable bodies and GetBody entirely under the forwarding owner's control.
func ForRequest(body io.ReadCloser, r *http.Request, target *url.URL, path, profile, upstream string) *Observer {
	escaped := strings.TrimSuffix(target.EscapedPath(), "/") + "/" + path
	return New(body, r.Context(), r.Method, escaped, "/"+path, profile, upstream, r.Header, r.ContentLength)
}
