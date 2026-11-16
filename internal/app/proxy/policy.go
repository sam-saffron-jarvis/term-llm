package proxy

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// validatePolicy checks syntax only: account existence and origin scope are
// validated by the trusted account integration before activation/dispatch.
func validatePolicy(r *Route) error {
	if r.Account != "" && !slug.MatchString(r.Account) {
		return fmt.Errorf("invalid account name")
	}
	if r.Redirects != "" && r.Redirects != "passthrough" && r.Redirects != "refuse" {
		return fmt.Errorf("redirects must be passthrough or refuse")
	}
	if r.Usage != "" && r.Usage != "off" && r.Usage != "auto" {
		return fmt.Errorf("usage must be off or auto")
	}
	if len(r.Allow) > 128 {
		return fmt.Errorf("at most 128 allow rules")
	}
	seen := map[string]bool{}
	for _, rule := range r.Allow {
		method, prefix, ok := strings.Cut(rule, " ")
		if !ok || !httpguts.ValidHeaderFieldName(method) || method != strings.ToUpper(method) || method == "CONNECT" || !canonicalPolicyPath(prefix) || seen[rule] {
			return fmt.Errorf("allow rules must be unique canonical METHOD /prefix entries")
		}
		seen[rule] = true
	}
	// Rule order has no meaning and must not cause needless regrants.
	r.Allow = append([]string(nil), r.Allow...)
	sort.Strings(r.Allow)
	return nil
}

func canonicalPolicyPath(p string) bool {
	return safePath(p) && !strings.ContainsAny(p, "%?# ") && (path.Clean(p) == p || p != "/" && path.Clean(p)+"/" == p)
}

// Policy paths are relative to the upstream base, not the local mount. Reject
// escaped paths for allowlisted routes rather than guessing how a vendor will
// decode separators, nested escapes or escaped method endpoint names.
func policyAllows(r Route, method, p string) bool {
	if len(r.Allow) == 0 {
		return true
	}
	if !canonicalPolicyPath(p) {
		return false
	}
	for _, rule := range r.Allow {
		m, prefix, _ := strings.Cut(rule, " ")
		if m == method && strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// ResponseBodyTap is an optional response-only observer seam. It must preserve
// bytes/backpressure and leave resp.Body with the same Close ownership. For 101 it
// must also preserve io.ReadWriteCloser and support concurrent Close/Read/Write.
// Installed before serving requests.
type ResponseBodyTap func(Route, *http.Response, *Record)
