package proxyclient

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

func normalizeURL(u *url.URL) error {
	if u.Scheme == "ws" {
		u.Scheme = "http"
	}
	if u.Scheme == "wss" {
		u.Scheme = "https"
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("invalid proxy URL")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid proxy port")
		}
	}
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return nil
}
func canonicalRoot(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("invalid proxy root")
	}
	if err = normalizeURL(u); err != nil {
		return "", err
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// SameRoot compares exact endpoint roots with normal URL authority equivalence,
// including default ports and WebSocket scheme counterparts. It never treats a
// child path as the same root.
func SameRoot(a, b string) bool {
	left, e1 := canonicalRoot(a)
	right, e2 := canonicalRoot(b)
	return e1 == nil && e2 == nil && left == right
}
