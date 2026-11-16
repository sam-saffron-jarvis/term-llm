package proxy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/term-llm/term-llm/internal/app/proxy/usagetap"
)

// ObservedUsage is a response-only snapshot. Protocol preserves the provider's
// cache/reasoning conventions: Messages and Bedrock input excludes cache reads
// and writes; Responses/Chat/Gemini input includes reads. OpenAI reasoning is
// already in output; Gemini thoughts are separate. Missing counts remain -1.
// Model/Tier are exact bounded response identities, or empty. Ambiguous usage
// must not be priced: mixed/unsafe identities are not a default model or tier.
type ObservedUsage struct {
	Usage    usagetap.Usage    `json:"usage"`
	Protocol usagetap.Protocol `json:"protocol"`
}

// ObserveResponseUsage is the Service.ResponseTap hook. The server must close
// the response body before reading or persisting the terminal Record. Usage is
// assigned only at Close, never while streaming or from a request body.
func ObserveResponseUsage(route Route, resp *http.Response, record *Record) {
	if record == nil {
		return
	}
	NewUsageObserver(route, resp, func(u ObservedUsage) { record.Usage = &u })
}

type usageTap interface {
	Feed([]byte)
	Finish(bool) usagetap.Usage
}

// NewUsageObserver replaces resp.Body with a bounded byte-preserving observer.
// It publishes exactly once, on Close, and preserves io.ReadWriteCloser for 101
// responses. publish must synchronously store the value before terminal ledger
// finish; its panic is isolated from forwarding. Install only on usage:auto.
//
// Request headers must negotiate identity encoding. Unexpected Content-Encoding
// and WS extensions are unknown; this observer does not decompress or launch
// goroutines. Completion requires EOF, no read failure, and no request-context
// cancellation. A WS close control frame without EOF is conservatively partial.
func NewUsageObserver(route Route, resp *http.Response, publish func(ObservedUsage)) {
	if resp == nil || resp.Body == nil {
		return
	}
	tap, protocol := selectUsageTap(route, resp)
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	body := &usageObserverBody{body: resp.Body, tap: tap, protocol: protocol, ctx: ctx, publish: publish}
	if enc := strings.TrimSpace(strings.Join(resp.Header.Values("Content-Encoding"), ",")); enc != "" && !strings.EqualFold(enc, "identity") {
		body.disabled = true
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		if strings.TrimSpace(strings.Join(resp.Header.Values("Sec-WebSocket-Extensions"), ",")) != "" {
			body.disabled = true
		}
		if duplex, ok := resp.Body.(io.ReadWriteCloser); ok {
			resp.Body = &usageObserverDuplex{usageObserverBody: body, writer: duplex}
			return
		}
		body.disabled = true
	}
	resp.Body = body
}

func selectUsageTap(route Route, resp *http.Response) (usageTap, usagetap.Protocol) {
	path := ""
	if resp.Request != nil && resp.Request.URL != nil {
		path = resp.Request.URL.Path
	}
	// Route.Upstream is a root, not the requested endpoint; never inspect bodies
	// or infer a protocol from a provider/account display name.
	_ = route
	ct := resp.Header.Get("Content-Type")
	if strings.EqualFold(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]), "application/vnd.amazon.eventstream") {
		return usagetap.NewEventStream(), usagetap.Bedrock
	}
	p := usagetap.ForResponse(path, ct)
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// WS upgrades usually have no Content-Type. Select only the endpoint schema.
		p = usagetap.ForResponse(path, "application/json")
		return usagetap.NewWebSocket(p), p.Protocol()
	}
	if strings.Contains(path, "/model/") && strings.HasSuffix(path, "/invoke") && p.Protocol() == "" {
		return usagetap.New(usagetap.Messages, usagetap.JSON), usagetap.Bedrock
	}
	return p, p.Protocol()
}

type usageObserverBody struct {
	body                              io.ReadCloser
	tap                               usageTap
	protocol                          usagetap.Protocol
	ctx                               context.Context
	publish                           func(ObservedUsage)
	mu                                sync.Mutex
	eof, readFailed, closed, disabled bool
	once                              sync.Once
	closeErr                          error
}

func (b *usageObserverBody) Read(p []byte) (n int, err error) {
	n, err = b.body.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return n, err
	}
	if n > 0 && !b.disabled {
		b.feed(p[:n])
	}
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.readFailed = true
	}
	return n, err
}
func (b *usageObserverBody) feed(data []byte) {
	defer func() {
		if recover() != nil {
			b.disabled = true
		}
	}()
	b.tap.Feed(data)
}
func unknownUsage() usagetap.Usage { return usagetap.New("", usagetap.JSON).Finish(false) }
func (b *usageObserverBody) finish() (u usagetap.Usage) {
	u = unknownUsage()
	defer func() {
		if recover() != nil {
			u = unknownUsage()
		}
	}()
	if !b.disabled {
		u = b.tap.Finish(b.eof && !b.readFailed && b.ctx.Err() == nil)
	}
	return u
}
func (b *usageObserverBody) Close() error {
	b.once.Do(func() {
		// Close underlying first to release any blocked Read. We never hold mu over
		// transport operations. A concurrent read that loses this race is omitted,
		// conservatively leaving partial/unknown evidence rather than a false total.
		b.closeErr = b.body.Close()
		b.mu.Lock()
		b.closed = true
		u := b.finish()
		b.tap = nil // release bounded parser buffers even after a recovered panic
		b.mu.Unlock()
		if b.publish != nil {
			func() { defer func() { _ = recover() }(); b.publish(ObservedUsage{Usage: u, Protocol: b.protocol}) }()
		}
	})
	return b.closeErr
}

type usageObserverDuplex struct {
	*usageObserverBody
	writer io.Writer
}

func (b *usageObserverDuplex) Write(p []byte) (int, error) { return b.writer.Write(p) }
