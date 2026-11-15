package proxy

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/term-llm/term-llm/internal/secrettext"
)

type Options struct {
	StateDir, AdminTokenFile, ConfigFile, PublicURL, Host string
	Port, MaxConcurrent, RetentionDays                    int
	MaxBody                                               int64
	RequestTimeout                                        time.Duration
}
type session struct {
	csrf    string
	expires time.Time
}
type Service struct {
	mu        sync.RWMutex
	store     *store
	snap      *snapshot
	opts      Options
	adminHash string
	transport *http.Transport
	slots     chan struct{}
	active    sync.Map
	authMu    sync.Mutex
	sessions  map[string]session
	attempts  map[string]attempt
}
type attempt struct {
	count int
	since time.Time
}

func Open(opts Options) (*Service, error) {
	if opts.StateDir == "" || opts.AdminTokenFile == "" {
		return nil, fmt.Errorf("--state-dir and --admin-token-file are required")
	}
	b, err := privateFile(opts.AdminTokenFile, 4096)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return nil, fmt.Errorf("admin token must have at least 32 non-whitespace characters; provision 256 random bits")
	}
	if opts.PublicURL != "" {
		u, e := url.Parse(opts.PublicURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("public URL must be an HTTPS origin")
		}
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if port := u.Port(); port != "" && port != "443" {
			host = net.JoinHostPort(u.Hostname(), port)
		}
		opts.PublicURL = "https://" + host
	}
	if opts.MaxConcurrent < 0 || opts.MaxBody < 0 || opts.RequestTimeout < 0 || opts.RetentionDays < 0 {
		return nil, fmt.Errorf("proxy bounds must be positive")
	}
	if opts.MaxBody > 1<<30 || opts.RequestTimeout > 24*time.Hour || opts.RetentionDays > 3650 {
		return nil, fmt.Errorf("proxy bounds exceed maximum (1 GiB body, 24h deadline, 3650 days retention)")
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 64
	}
	if opts.MaxConcurrent > 1024 {
		return nil, fmt.Errorf("maximum concurrency is 1024")
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = 64 << 20
	}
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = 15 * time.Minute
	}
	if opts.RetentionDays <= 0 {
		opts.RetentionDays = 30
	}
	st, err := openStore(opts.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Service{store: st, opts: opts, adminHash: hashToken(token), slots: make(chan struct{}, opts.MaxConcurrent), sessions: map[string]session{}, attempts: map[string]attempt{}}
	s.transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, DisableCompression: true, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 128, MaxIdleConnsPerHost: 16, MaxConnsPerHost: opts.MaxConcurrent, MaxResponseHeaderBytes: 1 << 20}
	if err = s.activateInitial(); err != nil {
		st.db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Service) activateInitial() error {
	c, rev, stored, err := s.store.load()
	if err != nil {
		return fmt.Errorf("load proxy state: %w", err)
	}
	if s.opts.ConfigFile != "" {
		c, err = fileConfig(s.opts.ConfigFile)
		if err != nil {
			return err
		}
	}
	secrets, err := resolveSecrets(c, stored, s.opts.AdminTokenFile, s.opts.ConfigFile != "")
	if err != nil {
		return err
	}
	snap, err := compile(c, rev+1, secrets)
	if err != nil {
		return err
	}
	if err = s.store.saveConfig(snap, s.opts.ConfigFile != ""); err != nil {
		return fmt.Errorf("activate proxy configuration: %w", err)
	}
	s.snap = snap
	return nil
}
func (s *Service) Close() error { s.transport.CloseIdleConnections(); return s.store.db.Close() }
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if s.store.db.PingContext(ctx) != nil {
			http.Error(w, "not ready", 503)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, "ok\n")
		return
	}
	if (r.URL.Path == "/_proxy" || strings.HasPrefix(r.URL.Path, "/_proxy/")) || r.URL.Path == "/" {
		s.admin(w, r)
		return
	}
	s.forward(w, r)
}
func (s *Service) forward(w http.ResponseWriter, r *http.Request) {
	if len(r.Method) > 64 || r.URL.IsAbs() || r.Host == "" || !safePath(r.URL.EscapedPath()) || r.Method == "CONNECT" || r.Header.Get("Upgrade") != "" || hasConnectionToken(r.Header, "upgrade") {
		http.Error(w, "unsupported request target or upgrade", 400)
		return
	}
	// Bound admission before authentication/store work; rejected traffic must not
	// create an unbounded queue waiting for SQLite or a configuration writer.
	select {
	case s.slots <- struct{}{}:
	default:
		http.Error(w, "proxy concurrency limit", 503)
		return
	}
	defer func() { <-s.slots }()
	// Hold the snapshot lock through authorization and committed start: an admin
	// mutation must not race between grant checking and admission. SQLite's busy
	// timeout bounds store waits; the admission slots also bound waiting readers.
	s.mu.RLock()
	var route *activeRoute
	for i := range s.snap.routes {
		candidate := &s.snap.routes[i]
		if candidate.Enabled && strings.HasPrefix(r.URL.EscapedPath(), candidate.LocalPrefix) {
			route = candidate
			break
		}
	}
	if route == nil {
		s.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	values := r.Header.Values(route.CallerAuth.Header)
	if len(values) != 1 || !strings.HasPrefix(values[0], route.CallerAuth.Prefix) {
		s.mu.RUnlock()
		http.Error(w, "unauthorized", 401)
		return
	}
	token := strings.TrimPrefix(values[0], route.CallerAuth.Prefix)
	if token == "" || subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(s.adminHash)) == 1 {
		s.mu.RUnlock()
		http.Error(w, "unauthorized", 401)
		return
	}
	caller, err := s.store.authorize(token, *route)
	if err != nil {
		s.mu.RUnlock()
		http.Error(w, "unauthorized or route not granted", 401)
		return
	}
	for key := range route.headers {
		if len(r.Header.Values(key)) > 1 {
			s.mu.RUnlock()
			http.Error(w, "duplicate credential header", 400)
			return
		}
	}
	if r.ContentLength > s.opts.MaxBody {
		s.mu.RUnlock()
		http.Error(w, "request body too large", 413)
		return
	}
	ar := *route
	snap := s.snap
	id := randomToken("px_")[:35]
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	path := strings.TrimPrefix(r.URL.EscapedPath(), ar.LocalPrefix)
	started := time.Now()
	rec := Record{ID: id, Started: started.UnixMilli(), CallerID: caller.ID, CallerName: caller.Name, Route: ar.Name, Revision: snap.revision, Method: r.Method, Path: s.redactPath("/"+path, token, snap), LocalPrefix: ar.LocalPrefix, Peer: peer, Outcome: "unknown"}
	if err = s.store.start(r.Context(), &rec); err != nil {
		s.mu.RUnlock()
		http.Error(w, "request ledger unavailable; not dispatched", 503)
		return
	}
	s.active.Store(id, true)
	s.mu.RUnlock()
	w.Header().Set("X-Term-LLM-Proxy-Request-ID", id)
	s.dispatch(w, r, ar, path, &rec, snap, started)
}
func hasConnectionToken(h http.Header, token string) bool {
	for _, v := range h.Values("Connection") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
func (s *Service) redactPath(path, token string, snap *snapshot) string {
	decoded, _ := url.PathUnescape(path)
	for _, value := range []string{token} {
		if value != "" {
			decoded = strings.ReplaceAll(decoded, value, "[redacted]")
		}
	}
	for _, m := range snap.secrets {
		if m.Value != "" {
			decoded = strings.ReplaceAll(decoded, m.Value, "[redacted]")
		}
	}
	// Decoded metadata avoids encoded known-secret bypasses; wire path remains untouched.
	decoded = secrettext.Redact(decoded)
	if len(decoded) > 2048 {
		decoded = decoded[:2048]
	}
	return decoded
}

type countingBody struct {
	io.ReadCloser
	n                atomic.Int64
	source, trailers http.Header
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	if err == io.EOF {
		for key := range b.trailers {
			b.trailers[key] = append([]string(nil), b.source.Values(key)...)
		}
	}
	return n, err
}

// cookieFreeBody also removes late-arriving cookies in response trailers.
type cookieFreeBody struct {
	io.ReadCloser
	response *http.Response
}

func (b *cookieFreeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.response.Trailer.Del("Set-Cookie")
	}
	return n, err
}

type responseStats struct {
	http.ResponseWriter
	status int
	bytes  int64
	first  time.Time
}

func (w *responseStats) WriteHeader(code int) {
	if code >= 200 && w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseStats) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.first.IsZero() {
		w.first = time.Now()
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}
func (w *responseStats) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseStats) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func stripHeaders(h http.Header, auth string) {
	h.Del(auth)
	for key := range h {
		lower := strings.ToLower(key)
		if forbiddenHeader(key) || lower == "authorization" || lower == "proxy-authorization" {
			h.Del(key)
		}
	}
}
func (s *Service) dispatch(w http.ResponseWriter, r *http.Request, ar activeRoute, path string, rec *Record, snap *snapshot, started time.Time) {
	ctx, cancel := context.WithDeadline(r.Context(), started.Add(s.opts.RequestTimeout))
	defer cancel()
	// Keep only declared safe trailers and populate them after the incoming body
	// has been read. Request.Clone otherwise loses late-arriving trailer values.
	source := r.Trailer
	trailers := source.Clone()
	stripHeaders(trailers, ar.CallerAuth.Header)
	for key := range ar.headers {
		trailers.Del(key)
	}
	for key := range trailers {
		trailers[key] = nil
	}
	r = r.Clone(ctx)
	// A non-replayable Body even for empty GET disables Transport's stale-connection retries.
	body := r.Body
	if body == nil || body == http.NoBody {
		body = io.NopCloser(strings.NewReader(""))
	}
	counter := &countingBody{ReadCloser: http.MaxBytesReader(w, body, s.opts.MaxBody), source: source, trailers: trailers}
	r.Body = counter
	r.GetBody = nil
	stats := &responseStats{ResponseWriter: w}
	outcome := "complete"
	defer func() {
		panicked := recover()
		if panicked != nil {
			outcome = "incomplete"
		}
		if ctx.Err() != nil {
			outcome = "canceled_or_timeout"
		}
		rec.Status = stats.status
		rec.Sent = counter.n.Load()
		rec.Received = stats.bytes
		rec.Ended = time.Now().UnixMilli()
		rec.TotalMS = time.Since(started).Milliseconds()
		rec.Outcome = outcome
		if !stats.first.IsZero() {
			rec.FirstByteMS = stats.first.Sub(started).Milliseconds()
		}
		if err := s.store.finish(*rec); err != nil {
			log.Print("proxy: terminal ledger write failed; outcome remains unknown")
		}
		s.active.Delete(rec.ID)
		if panicked != nil {
			panic(panicked)
		}
	}()
	proxy := httputil.ReverseProxy{
		Transport: s.transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0),
		Rewrite: func(pr *httputil.ProxyRequest) {
			escaped := strings.TrimSuffix(ar.target.EscapedPath(), "/") + "/" + path
			decoded, _ := url.PathUnescape(escaped)
			pr.Out.URL = &url.URL{Scheme: ar.target.Scheme, Host: ar.target.Host, Path: decoded, RawPath: escaped, RawQuery: pr.In.URL.RawQuery, ForceQuery: pr.In.URL.ForceQuery}
			pr.Out.Host = ar.target.Host
			// ReverseProxy clears zero-length bodies before Rewrite; restore the
			// non-replayable sentinel so even empty safe-method requests cannot retry.
			pr.Out.Body = counter
			pr.Out.Trailer = trailers
			pr.Out.GetBody = nil
			stripHeaders(pr.Out.Header, ar.CallerAuth.Header)
			for key, values := range ar.headers {
				pr.Out.Header[key] = append([]string(nil), values...)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			rec.HeaderMS = time.Since(started).Milliseconds()
			resp.Header.Del("Set-Cookie")
			resp.Trailer.Del("Set-Cookie")
			resp.Body = &cookieFreeBody{ReadCloser: resp.Body, response: resp}
			resp.Header.Set("X-Term-LLM-Proxy-Request-ID", rec.ID)
			if resp.StatusCode >= 400 {
				outcome = "http_error"
			}
			if ar.RequestIDHeader != "" {
				v := resp.Header.Get(ar.RequestIDHeader)
				rec.UpstreamID = secrettext.Redact(v)
				for _, m := range snap.secrets {
					if m.Value != "" {
						rec.UpstreamID = strings.ReplaceAll(rec.UpstreamID, m.Value, "[redacted]")
					}
				}
				if len(rec.UpstreamID) > 256 {
					rec.UpstreamID = rec.UpstreamID[:256]
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			outcome = "transport_error"
			http.Error(w, "upstream connection failed; outcome may be unknown", 502)
		},
	}
	proxy.ServeHTTP(stats, r)
}
