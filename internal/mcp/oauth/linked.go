package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Linked connections never share account registrations or refresh tokens with
// the host or another installer, even if URLs are identical.
var linkedCoordinators sync.Map

func ForInstaller(id string) *Coordinator {
	if id == "" {
		return DefaultCoordinator()
	}
	path, err := installerStorePath(id)
	if c, ok := linkedCoordinators.Load(path); ok {
		return c.(*Coordinator)
	}
	c := NewCoordinator(NewFileStore(path))
	c.initErr = err
	c.client = &http.Client{Transport: LinkedTransport(http.DefaultTransport.(*http.Transport))}
	v, _ := linkedCoordinators.LoadOrStore(path, c)
	return v.(*Coordinator)
}

// OwnerFlowCoordinator also finds delegated flows for the local CLI and TUI.
func OwnerFlowCoordinator(id string) *Coordinator {
	c := DefaultCoordinator()
	if _, ok := c.Flow(id); ok {
		return c
	}
	linkedCoordinators.Range(func(_, v any) bool {
		candidate := v.(*Coordinator)
		if _, ok := candidate.Flow(id); ok {
			c = candidate
			return false
		}
		return true
	})
	return c
}
func CompletePublicCallback(state, code, issuer, oauthError string) (id string, ok bool) {
	id, ok = DefaultCoordinator().CompleteCallback(state, code, issuer, oauthError)
	if !ok {
		linkedCoordinators.Range(func(_, v any) bool {
			id, ok = v.(*Coordinator).CompleteCallback(state, code, issuer, oauthError)
			return !ok
		})
	}
	return
}

var reservedRanges = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("3fff::/20"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16")}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip)) {
		return false
	}
	for _, p := range reservedRanges {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func PublicHTTPS(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicAddress(ip)
	}
	return strings.Contains(host, ".") && !strings.HasSuffix(host, ".localhost")
}

type linkedTransport struct{ inner http.RoundTripper }

func (t linkedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !PublicHTTPS(r.URL) {
		return nil, fmt.Errorf("MCP requires a public HTTPS endpoint")
	}
	return t.inner.RoundTrip(r)
}

// LinkedTransport enforces the destination at dial time for MCP, OAuth
// discovery, token exchange, redirects and refreshes; never use host proxies.
func LinkedTransport(base *http.Transport) http.RoundTripper {
	tr := base.Clone()
	tr.Proxy = nil
	dial := tr.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicAddress(ip) {
				return nil, fmt.Errorf("MCP destination is not public")
			}
		}
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("public MCP endpoint unavailable")
	}
	return linkedTransport{tr}
}
