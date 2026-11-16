package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const websocketLifetime = time.Hour

type activeSocket struct {
	caller, route, authority, account string
	cancel                            context.CancelCauseFunc
}

// Caller/grant/config mutations call these while holding s.mu, serializing
// cancellation with admission. Pending handshakes are included, not just 101s.
func (s *Service) cancelCallerSockets(caller string) {
	s.sockets.Range(func(_, v any) bool {
		a := v.(*activeSocket)
		if a.caller == caller {
			a.cancel(errSocketRevoked)
		}
		return true
	})
}

// cancelAccountSockets is the account integration hook for sign-out, replacement
// or login-needed transitions. Call under s.mu after the state commit.
func (s *Service) cancelAccountSockets(account string) {
	if account == "" {
		return
	}
	s.sockets.Range(func(_, v any) bool {
		a := v.(*activeSocket)
		if a.account == account {
			a.cancel(errSocketRevoked)
		}
		return true
	})
}

func (s *Service) reconcileSockets(next *snapshot) {
	s.sockets.Range(func(_, v any) bool {
		a := v.(*activeSocket)
		for _, r := range next.routes {
			if r.Name == a.route && r.Enabled && r.authority == a.authority {
				return true
			}
		}
		a.cancel(errSocketRevoked)
		return true
	})
}
func (s *Service) closeSockets() {
	s.mu.Lock()
	s.closing = true
	s.sockets.Range(func(_, v any) bool { v.(*activeSocket).cancel(errSocketShutdown); return true })
	s.mu.Unlock()
	s.socketWG.Wait()
}

type socketCause string

func (e socketCause) Error() string { return string(e) }

const (
	errSocketRevoked  socketCause = "closed_by_revocation"
	errSocketShutdown socketCause = "closed_by_shutdown"
)

// A separate HTTP/1 transport is required even when an HTTP/2 connection to the
// same vendor is already pooled. Never negotiate h2 for a classic Upgrade.
func websocketTransport(base *http.Transport) *http.Transport {
	t := base.Clone()
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return t
}

// Counting the upstream duplex stream preserves raw frames/control messages.
// Cancellation closes it explicitly: net/http does not own hijacked sockets.
type countingSocket struct {
	io.ReadWriteCloser
	sent, received atomic.Int64
	ended          atomic.Int32
	stop           func() bool
	closeOnce      sync.Once
	closeErr       error
}

func (b *countingSocket) Read(p []byte) (int, error) {
	n, e := b.ReadWriteCloser.Read(p)
	b.received.Add(int64(n))
	if e != nil {
		b.ended.CompareAndSwap(0, 2)
	}
	return n, e
}
func (b *countingSocket) Write(p []byte) (int, error) {
	n, e := b.ReadWriteCloser.Write(p)
	b.sent.Add(int64(n))
	return n, e
}
func (b *countingSocket) Close() error {
	b.ended.CompareAndSwap(0, 1)
	if b.stop != nil {
		b.stop()
	}
	return b.closeBody()
}
func (b *countingSocket) closeBody() error {
	b.closeOnce.Do(func() { b.closeErr = b.ReadWriteCloser.Close() })
	return b.closeErr
}

func validWebsocketRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && r.ProtoMajor == 1 && r.ProtoMinor == 1 && len(r.Header.Values("Upgrade")) == 1 && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && hasConnectionToken(r.Header, "upgrade")
}

// upgradedConn intentionally exposes net.Conn but not TCP CloseWrite. A
// WebSocket EOF ends the whole connection; ReverseProxy's TCP half-close mode
// otherwise waits for the peer indefinitely after a clean upstream EOF.
type upgradedConn struct{ net.Conn }
