package proxyclient

import "strings"

// Related returns another explicitly registered route of the same proxy and
// principal. It never constructs an unregistered target or widens a grant.
func Related(endpoint, route string) (Binding, bool) {
	source, ok := Lookup(endpoint)
	if !ok {
		return Binding{}, false
	}
	i := strings.LastIndex(source.Root, "/")
	if i < 0 {
		return Binding{}, false
	}
	target := source.Root[:i+1] + route
	registry.RLock()
	defer registry.RUnlock()
	b, ok := registry.bindings[target]
	return b, ok && b.Token == source.Token && b.Kind == source.Kind
}

// KindActive lets unscoped metadata-only paths avoid reading a local vendor
// credential while a delegated integration is active. It does not select auth
// for a network request; those must use Lookup with an explicit endpoint.
func KindActive(kind string) bool {
	registry.RLock()
	defer registry.RUnlock()
	for _, b := range registry.bindings {
		if b.Kind == kind {
			return true
		}
	}
	return false
}
