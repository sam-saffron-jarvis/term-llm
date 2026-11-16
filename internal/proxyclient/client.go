// Package proxyclient tracks explicit, endpoint-bound delegated proxy credentials for native clients.
// Registration is process-local and never rewrites arbitrary HTTP traffic or enforces OS egress.
package proxyclient

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Binding is an explicit credential delegation to an API root. Tokens are never serialized.
type Binding struct {
	Root  string
	Kind  string
	Token string `json:"-"`
}

var registry struct {
	sync.RWMutex
	bindings map[string]Binding
}

// Register remembers roots for the life of the process. Removed or stale proxy
// roots must never fall back to a local vendor credential during reload. Running
// roots retain one principal for the process lifetime; restart to switch caller identity.
func Register(bindings []Binding) error {
	registry.Lock()
	defer registry.Unlock()
	pending := make(map[string]Binding, len(bindings))
	for _, b := range bindings {
		root, err := canonicalRoot(b.Root)
		if err != nil || b.Token == "" {
			return fmt.Errorf("invalid delegated proxy binding")
		}
		b.Root = root
		if old, ok := pending[root]; ok && (old.Kind != b.Kind || old.Token != b.Token) {
			return fmt.Errorf("proxy API root cannot represent conflicting credential kinds or principals")
		}
		if old, ok := registry.bindings[root]; ok && (old.Kind != b.Kind || old.Token != b.Token) {
			return fmt.Errorf("proxy API root is already bound to another credential kind or caller; restart after changing proxy client identity")
		}
		pending[root] = b
	}
	newSize := len(registry.bindings)
	for root := range pending {
		if _, exists := registry.bindings[root]; !exists {
			newSize++
		}
	}
	if newSize > 4096 {
		return fmt.Errorf("too many process-local proxy bindings; restart after changing routing configuration")
	}
	if registry.bindings == nil {
		registry.bindings = make(map[string]Binding)
	}
	for root, b := range pending {
		registry.bindings[root] = b
	}
	return nil
}

// Lookup returns only a registered API root or an operation below that root.
// Host, port, scheme and path boundaries all participate; substring matches do not.
func Lookup(endpoint string) (Binding, bool) {
	u, err := url.Parse(endpoint)
	if err != nil || normalizeURL(u) != nil {
		return Binding{}, false
	}
	registry.RLock()
	defer registry.RUnlock()
	var best Binding
	for _, b := range registry.bindings {
		r, _ := url.Parse(b.Root)
		if r == nil || !strings.EqualFold(u.Host, r.Host) || u.Scheme != r.Scheme {
			continue
		}
		base := strings.TrimRight(r.EscapedPath(), "/")
		path := u.EscapedPath()
		if (path == base || strings.HasPrefix(path, base+"/")) && len(b.Root) > len(best.Root) {
			best = b
		}
	}
	return best, best.Root != ""
}

// Do preserves the client's transport and timeouts, but never follows a redirect
// originating at a delegated root. Custom-header credentials otherwise survive
// Go's ordinary cross-host redirect handling.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	binding, bound := Lookup(req.URL.String())
	if !bound {
		return client.Do(req)
	}
	copyRequest := req.Clone(req.Context())
	copyRequest.Header = Annotate(req.URL.String(), req.Header)
	req = copyRequest
	// Native Ollama and public catalog operations have no vendor auth field.
	// They still need the scoped caller token at the proxy. Do not add a second
	// auth form when the integration already supplied one.
	if req.Header.Get("Authorization") == "" && req.Header.Get("x-api-key") == "" && req.Header.Get("x-goog-api-key") == "" && req.Header.Get("xi-api-key") == "" && req.Header.Get("x-key") == "" {
		req.Header.Set("Authorization", "Bearer "+binding.Token)
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return copy.Do(req)
}
