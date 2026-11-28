package proxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/term-llm/term-llm/internal/app/proxy/nativeusage/poll"
	"github.com/term-llm/term-llm/internal/modelsdev"
	"github.com/term-llm/term-llm/internal/passkeyauth"
	"github.com/term-llm/term-llm/internal/secrettext"
)

type Options struct {
	DebugPayloadDir                                           string
	DebugPayloadMaxBytes, DebugPayloadMaxTotalBytes           int64
	autoBootstrap                                             bool
	StateDir, AdminTokenFile, ConfigFile, PublicURL, Host     string
	Auth, PasskeyBootstrapTokenFile, PasskeyRecoveryTokenFile string
	Port, MaxConcurrent, RetentionDays                        int
	MaxBody                                                   int64
	RequestTimeout                                            time.Duration
}
type session struct {
	csrf      string
	expires   time.Time
	principal passkeyauth.Principal
}
type Service struct {
	pricing           *modelsdev.Client
	nativeUsage       *poll.Cache
	payloadDebug      *payloadDebugger
	debugMu           sync.Mutex // guards lazy capture initialization and shutdown
	debugClosing      bool
	closeOnce         sync.Once
	closeErr          error
	mu                sync.RWMutex
	store             *store
	snap              *snapshot
	opts              Options
	adminHash         string
	passkey           *proxyPasskeys
	transport         *http.Transport
	copyBuffers       *copyBufferPool
	slots             chan struct{}
	active            sync.Map
	sockets           sync.Map
	accountLocks      sync.Map
	accountRejected   sync.Map
	accountRedactions sync.Map
	claudeNative      *claudeForwarder
	agyNative         *claudeForwarder
	deviceSetup       deviceSetups
	socketWG          sync.WaitGroup
	closing           bool
	// ResponseTap observes opt-in responses; set before serving.
	ResponseTap  ResponseBodyTap
	authMu       sync.Mutex
	sessions     map[string]session
	attempts     map[string]attempt
	manageGate   manageAttempts
	adminKeyUses adminKeyRegistry
}
type attempt struct {
	count int
	since time.Time
}

func Open(opts Options) (*Service, error) {
	if _, _, err := debugPayloadLimits(opts); err != nil {
		return nil, err
	}
	adminHash, err := prepareAuthOptions(&opts)
	if err != nil {
		return nil, err
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
	// The operator's file is the source of route identity for a file-mode
	// upgrade. Parse it before opening SQLite and activate the same snapshot.
	var file *Config
	if opts.ConfigFile != "" {
		c, err := fileConfig(opts.ConfigFile)
		if err != nil {
			return nil, err
		}
		file = &c
	}
	st, err := openStore(opts.StateDir, file)
	if err != nil {
		return nil, err
	}
	keyUses, err := st.loadAdminKeyUses()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("load proxy admin API keys: %w", err), st.close())
	}
	s := &Service{store: st, opts: opts, adminHash: adminHash, copyBuffers: newCopyBufferPool(opts.MaxConcurrent), slots: make(chan struct{}, opts.MaxConcurrent), sessions: map[string]session{}, attempts: map[string]attempt{}, adminKeyUses: adminKeyRegistry{uses: keyUses}}
	s.transport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, DisableCompression: true, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 128, MaxIdleConnsPerHost: 16, MaxConnsPerHost: opts.MaxConcurrent, MaxResponseHeaderBytes: 1 << 20}
	s.payloadDebug, err = openPayloadDebugger(opts)
	if err != nil {
		s.Close()
		return nil, err
	}

	if opts.Auth == "passkey" {
		s.passkey, err = openProxyPasskeys(opts)
		if err != nil {
			s.Close()
			return nil, err
		}
	}
	if err = s.store.interruptAccounts(context.Background()); err != nil {
		s.Close()
		return nil, fmt.Errorf("recover account refresh fence: %w", errAccountUnavailable)
	}
	infos, e := s.store.accountInfos()
	if e != nil {
		s.Close()
		return nil, errAccountUnavailable
	}
	for _, info := range infos {
		a, e := s.store.account(info.Name)
		if e != nil {
			s.Close()
			return nil, errAccountUnavailable
		}
		s.rememberAccount(a.Name, a.credentials)
	}
	if err = s.activateInitial(file); err != nil {
		s.Close()
		return nil, err
	}
	// Recover older/crashed request evidence before Run can delete technical
	// history. Keep the full independent 400-day money retention window.
	now := time.Now()
	recovery, cancelRecovery := context.WithTimeout(context.Background(), 30*time.Second)
	err = s.store.reconcileMoneyHistory(recovery, now.AddDate(0, 0, -400).UnixMilli(), now.UnixMilli())
	cancelRecovery()
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("recover retained money history: %w", err)
	}
	s.pricing = modelsdev.NewClient(filepath.Join(opts.StateDir, "pricing"), nil)
	_ = s.pricing.Load() // Cached-only startup; a missing catalog never blocks serving.
	s.ResponseTap = ObserveResponseUsage
	s.openNativeUsage()
	return s, nil
}
func (s *Service) activateInitial(file *Config) error {
	c, rev, stored, err := s.store.load()
	if err != nil {
		return fmt.Errorf("load proxy state: %w", err)
	}
	if file != nil {
		c = *file
	}
	if err := s.bootstrapAccounts(context.Background(), c); err != nil {
		return err
	}
	secrets, err := s.resolveSecrets(c, stored, s.opts.ConfigFile != "")
	if err != nil {
		return err
	}
	snap, err := compile(c, rev+1, secrets)
	if err != nil {
		return err
	}
	if err = s.validateAccounts(snap); err != nil {
		return err
	}
	if err = s.store.saveConfig(snap, s.opts.ConfigFile != ""); err != nil {
		return fmt.Errorf("activate proxy configuration: %w", err)
	}
	s.snap = snap
	return nil
}
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.nativeUsage.Close()
		s.debugMu.Lock()
		s.debugClosing = true
		debugErr := s.payloadDebug.Close()
		s.debugMu.Unlock()
		s.deviceSetup.close()
		s.closeSockets()
		s.transport.CloseIdleConnections()
		var authErr error
		if s.passkey != nil {
			if s.passkey.generatedBootstrap != "" {
				_ = os.Remove(s.passkey.generatedBootstrap)
			}
			authErr = s.passkey.unlock()
		}
		s.closeErr = errors.Join(authErr, debugErr, s.store.close())
	})
	return s.closeErr
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if path, ok := strings.CutPrefix(r.URL.Path, "/_proxy/client/"); ok {
		switch path {
		case "v1/whoami":
			s.serveClientQuery(w, r, s.whoami)
		case "v1/providers":
			http.Error(w, "proxy discovery v1 was removed; upgrade term-llm", http.StatusGone)
		case "v2/providers":
			s.serveClientProviders(w, r)
		default:
			// An unavailable binding is a base URL, so callers can append paths.
			tail, unavailable := strings.CutPrefix(path, "unavailable/")
			id, _, _ := strings.Cut(tail, "/")
			if unavailable && slug.MatchString(id) {
				http.Error(w, id+" is not granted to this client", http.StatusForbidden)
			} else {
				http.NotFound(w, r)
			}
		}
		return
	}
	// The management namespace must not fall through to Claude discovery or CONNECT.
	managed := strings.HasPrefix(r.URL.Path, managePrefix)
	if !managed && s.claudeForward(w, r) {
		return
	}
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
	if managed {
		s.manageAPI(w, r)
		return
	}
	if (r.URL.Path == "/_proxy" || strings.HasPrefix(r.URL.Path, "/_proxy/")) || r.URL.Path == "/" {
		s.admin(w, r)
		return
	}
	s.forward(w, r)
}
func (s *Service) forward(w http.ResponseWriter, r *http.Request) {
	if len(r.Method) > 64 || r.URL.IsAbs() || r.Host == "" || !safePath(r.URL.EscapedPath()) || r.Method == "CONNECT" {
		http.Error(w, "unsupported request target or upgrade", 400)
		return
	}
	upgrade := len(r.Header.Values("Upgrade")) != 0 || hasConnectionToken(r.Header, "upgrade")
	if upgrade && !validWebsocketRequest(r) {
		http.Error(w, "unsupported upgrade", 400)
		return
	}
	// Bound admission before authentication/store work; rejected traffic must not
	// create an unbounded queue waiting for SQLite or a configuration writer.
	if !s.requestSlot(w) {
		return
	}
	defer func() { <-s.slots }()
	// Hold the snapshot lock through authorization and committed start: an admin
	// mutation must not race between grant checking and admission. SQLite's busy
	// timeout bounds store waits; the admission slots also bound waiting readers.
	s.mu.RLock()
	if s.closing {
		s.mu.RUnlock()
		http.Error(w, "proxy shutting down", 503)
		return
	}
	route := s.snap.selectRoute(r.URL.EscapedPath())
	if route == nil {
		s.mu.RUnlock()
		http.NotFound(w, r)
		return
	}
	if !claudeForwardRouteMatches(r, *route) {
		s.mu.RUnlock()
		http.Error(w, "native route scope changed", http.StatusUnauthorized)
		return
	}
	if upgrade && !route.WebSocket {
		s.mu.RUnlock()
		http.Error(w, "unsupported upgrade", 400)
		return
	}
	token := callerCredential(r.Header, route.CallerAuth.Header, route.CallerAuth.Prefix)
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
	if route.Account != "" && !validAccountHeaders(r.Header) {
		s.mu.RUnlock()
		http.Error(w, "duplicate account credential or identity header", 400)
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
	rec := Record{App: s.redactPath(boundedAttribution(r.Header.Get("X-Term-LLM-Proxy-App")), token, snap), ID: id, Started: started.UnixMilli(), CallerID: caller.ID, CallerName: caller.Name, Tag: caller.Tag, Route: ar.Name, Revision: snap.revision, Method: r.Method, Path: s.redactPath("/"+path, token, snap), LocalPrefix: ar.LocalPrefix, Peer: peer, Outcome: "unknown"}
	rec.Money = pendingRequestMoney(ar.Route.Billing)
	if err = s.store.start(r.Context(), &rec); err != nil {
		s.mu.RUnlock()
		http.Error(w, "request ledger unavailable; not dispatched", 503)
		return
	}
	s.active.Store(id, true)
	if upgrade {
		ctx, cancel := context.WithCancelCause(r.Context())
		defer cancel(nil)
		r = r.Clone(ctx)
		s.sockets.Store(id, &activeSocket{caller: caller.ID, route: ar.Name, authority: ar.authority, account: ar.Account, cancel: cancel})
		s.socketWG.Add(1)
		defer func() { s.sockets.Delete(id); s.socketWG.Done() }()
	}
	s.mu.RUnlock()
	w.Header().Set("X-Term-LLM-Proxy-Request-ID", id)
	path = ar.upstreamPath(r.URL.EscapedPath())
	if !s.routePolicyAllows(ar, r.Method, "/"+path) {
		http.Error(w, "route policy denied request", 403)
		rec.Status, rec.Outcome, rec.Ended = 403, "denied", time.Now().UnixMilli()
		rec.TotalMS = time.Since(started).Milliseconds()
		s.finalizeMoney(&rec, ar.Route)
		if err := s.store.finish(rec); err != nil {
			log.Print("proxy: denied ledger write failed")
		}
		s.active.Delete(id)
		return
	}
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
	for _, value := range []string{token, distinctCallerSecret(token)} {
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
	decoded = s.redactAccountMaterial(decoded)
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
func (w *responseStats) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.status, w.first = http.StatusSwitchingProtocols, time.Now()
	}
	if err == nil {
		conn = &upgradedConn{Conn: conn}
	}
	return conn, rw, err
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
	timeout := s.opts.RequestTimeout
	upgrade := validWebsocketRequest(r) && ar.WebSocket
	if upgrade && timeout > websocketLifetime {
		timeout = websocketLifetime
	}
	parent := r.Context()
	var streamCancel context.CancelCauseFunc
	trackedStream := false
	if !upgrade && (ar.Account != "" || ar.Usage == "auto") {
		parent, streamCancel = context.WithCancelCause(parent)
		defer streamCancel(nil)
		defer func() {
			if trackedStream {
				s.sockets.Delete(rec.ID)
				s.socketWG.Done()
			}
		}()
	}
	ctx, cancel := context.WithDeadline(parent, started.Add(timeout))
	defer cancel()
	// SigV4 must buffer before dispatch. Bound the server-side body read too;
	// canceling a context alone does not interrupt a blocked incoming Body.Read.
	if ar.Account != "" {
		if a, e := s.store.account(ar.Account); e == nil && a.Kind == "aws-sigv4" {
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(started.Add(timeout))
			defer controller.SetReadDeadline(time.Time{})
		}
	}
	// Keep only declared safe trailers and populate them after the incoming body
	// has been read. Request.Clone otherwise loses late-arriving trailer values.
	source := r.Trailer
	trailers := source.Clone()
	stripRouteHeaders(trailers, ar)
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

	capture := s.beginPayloadCapture(*rec, r)
	if capture != nil {
		body = &payloadReadCloser{ReadCloser: body, capture: capture, stream: "request.body"}
	}
	counter := &countingBody{ReadCloser: http.MaxBytesReader(w, body, s.opts.MaxBody), source: source, trailers: trailers}
	r.Body = counter
	requestObserver := observeRequestBody(counter, r, ar, path)
	if requestObserver != nil {
		r.Body = requestObserver
	}
	outgoingBody := r.Body
	r.GetBody = nil
	stats := &responseStats{ResponseWriter: w}
	outcome := "complete"
	var socket *countingSocket
	var finishTurns func() bool
	var custody *accountTransport
	defer func() {
		panicked := recover()
		// ReverseProxy closes an upgraded backend asynchronously. Close it here too
		// (idempotently) so observer finalization precedes the terminal ledger write.
		if socket != nil {
			_ = socket.Close()
		}
		if panicked != nil {
			outcome = "incomplete"
		}
		if ctx.Err() != nil {
			outcome = "canceled_or_timeout"
		}
		if socket != nil && ctx.Err() == nil {
			outcome = "closed_by_client"
			if socket.ended.Load() == 2 {
				outcome = "closed_by_upstream"
			}
		}
		if upgrade || streamCancel != nil {
			if cause := context.Cause(ctx); cause == errSocketRevoked || cause == errSocketShutdown {
				outcome = cause.Error()
			} else if upgrade && ctx.Err() == context.DeadlineExceeded {
				outcome = "lifetime_exceeded"
			}
		}
		if custody != nil && !custody.dispatched {
			outcome = "not_dispatched"
		}
		if requestObserver != nil {
			metadata := requestObserver.Finalize()
			rec.RequestMeta = &metadata
		}
		rec.Status = stats.status
		rec.Sent = counter.n.Load()
		rec.Received = stats.bytes
		if socket != nil {
			rec.Sent += socket.sent.Load()
			rec.Received += socket.received.Load()
		}
		rec.Ended = time.Now().UnixMilli()
		rec.TotalMS = time.Since(started).Milliseconds()
		rec.Outcome = outcome
		if !stats.first.IsZero() {
			rec.FirstByteMS = stats.first.Sub(started).Milliseconds()
		}
		capture.finish(*rec)
		if finishTurns == nil || !finishTurns() {
			s.finishDispatchRecord(rec, ar.Route)
		}
		s.active.Delete(rec.ID)
		if panicked != nil {
			panic(panicked)
		}
	}()
	baseTransport := s.transport
	if upgrade {
		baseTransport = websocketTransport(s.transport)
		defer baseTransport.CloseIdleConnections()
	}
	transport := s.debugTransport(baseTransport, capture, r, ar, snap)
	if ar.Account != "" {
		custody = &accountTransport{service: s, base: transport, route: ar}
		transport = custody
	}
	proxy := httputil.ReverseProxy{
		Transport: transport, BufferPool: s.copyBuffers, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0),
		Rewrite: func(pr *httputil.ProxyRequest) {
			escaped := strings.TrimSuffix(ar.target.EscapedPath(), "/") + "/" + path
			decoded, _ := url.PathUnescape(escaped)
			pr.Out.URL = &url.URL{Scheme: ar.target.Scheme, Host: ar.target.Host, Path: decoded, RawPath: escaped, RawQuery: pr.In.URL.RawQuery, ForceQuery: pr.In.URL.ForceQuery}
			pr.Out.Host = ar.target.Host
			// ReverseProxy clears zero-length bodies before Rewrite; restore the
			// non-replayable sentinel so even empty safe-method requests cannot retry.
			pr.Out.Body = outgoingBody
			pr.Out.Trailer = trailers
			pr.Out.GetBody = nil
			stripRouteHeaders(pr.Out.Header, ar)
			if upgrade {
				pr.Out.Header.Set("Connection", "Upgrade")
				pr.Out.Header.Set("Upgrade", "websocket")
			}
			if ar.Usage == "auto" {
				pr.Out.Header.Del("Sec-WebSocket-Extensions")
				pr.Out.Header.Set("Accept-Encoding", "identity")
			}
			for key, values := range ar.headers {
				pr.Out.Header[key] = append([]string(nil), values...)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			rec.HeaderMS = time.Since(started).Milliseconds()
			if streamCancel != nil && livePolicyStream(ar.Route, resp) {
				if err := s.trackPolicyStream(ar, rec, streamCancel, custody); err != nil {
					return err
				}
				trackedStream = true
			}
			resp.Header.Del("Set-Cookie")
			resp.Trailer.Del("Set-Cookie")
			if ar.Redirects == "refuse" && resp.StatusCode >= 300 && resp.StatusCode < 400 {
				resp.Body.Close()
				resp.StatusCode, resp.Status = 502, "502 Bad Gateway"
				resp.Header = http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "X-Term-LLM-Proxy-Redirect": {"refused"}}
				resp.Trailer = nil
				resp.Body = io.NopCloser(strings.NewReader("upstream redirect refused\n"))
				resp.ContentLength = -1
				resp.TransferEncoding = nil
			}
			if s.ResponseTap != nil && ar.Usage == "auto" {
				s.ResponseTap(ar.Route, resp, rec)
			}
			if resp.StatusCode == http.StatusSwitchingProtocols {
				duplex, ok := resp.Body.(io.ReadWriteCloser)
				if !upgrade || !ok {
					return fmt.Errorf("unsupported upstream upgrade")
				}
				finishTurns = s.logWebSocketTurns(resp, *rec, ar.Route)
				capture.wrapResponse(resp)
				duplex = resp.Body.(io.ReadWriteCloser)
				socket = &countingSocket{ReadWriteCloser: duplex}
				socket.stop = context.AfterFunc(ctx, func() { _ = socket.closeBody() })
				resp.Body = socket
			} else {
				capture.wrapResponse(resp)
				resp.Body = &cookieFreeBody{ReadCloser: resp.Body, response: resp}
			}
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
				if custody != nil {
					rec.UpstreamID = s.redactAccountMaterial(rec.UpstreamID, custody.used...)
				}
				if len(rec.UpstreamID) > 256 {
					rec.UpstreamID = rec.UpstreamID[:256]
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			outcome = "transport_error"
			if errors.Is(err, errAccountUnavailable) {
				outcome = "upstream_auth"
				http.Error(w, "trusted account unavailable; sign in or check configuration", 502)
				return
			}
			http.Error(w, "upstream connection failed; outcome may be unknown", 502)
		},
	}
	proxy.ServeHTTP(stats, r)
}
