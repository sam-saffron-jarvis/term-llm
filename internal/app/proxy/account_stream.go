package proxy

import (
	"context"
	"net/http"
	"strings"
)

func livePolicyStream(route Route, resp *http.Response) bool {
	if route.Account == "" && route.Usage != "auto" {
		return false
	}
	media, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.EqualFold(strings.TrimSpace(media), "text/event-stream")
}

// Track an opt-in SSE only after its response is known to be a live stream.
// Recheck admission under the same lock as admin mutations, so revocation while
// waiting for upstream headers cannot create a new uncancellable paid stream.
// Legacy opaque HTTP routes retain their admission-only revocation semantics.
func (s *Service) trackPolicyStream(route activeRoute, rec *Record, cancel context.CancelCauseFunc, custody *accountTransport) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closing {
		cancel(errSocketShutdown)
		return errSocketShutdown
	}
	valid := false
	for _, r := range s.snap.routes {
		if r.Name == route.Name && r.Enabled && r.authority == route.authority {
			valid = true
			break
		}
	}
	if route.Account != "" {
		a, err := s.store.account(route.Account)
		// A routine token rotation is not a sign-out. The identity epoch changes
		// only for an explicit replacement/revocation, not a successful refresh.
		sharedIdentity := custody != nil && (a.State == "ready" || a.State == "refreshing") && a.identityVersion == custody.identityVersion
		if err != nil || !sharedIdentity {
			valid = false
		}
	}
	var caller string
	if !valid || s.store.streamGrantStmt.QueryRow(rec.CallerID, route.Name, route.authority).Scan(&caller) != nil {
		cancel(errSocketRevoked)
		return errSocketRevoked
	}
	s.sockets.Store(rec.ID, &activeSocket{caller: rec.CallerID, route: route.Name, authority: route.authority, account: route.Account, cancel: cancel})
	s.socketWG.Add(1)
	return nil
}
