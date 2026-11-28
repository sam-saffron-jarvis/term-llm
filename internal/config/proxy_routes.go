package config

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

type discoveredProxyRoute struct {
	provider proxyDiscoveredProvider
	route    proxyDiscoveredRoute
	siblings []proxyDiscoveredRoute
}

func (r discoveredProxyRoute) endpoint(root string) string {
	return root + strings.TrimRight(r.route.LocalPrefix, "/")
}

func (r discoveredProxyRoute) identity() proxyDiscoveredDefault {
	return proxyDiscoveredDefault{Provider: r.provider.ID, Route: r.route.Name}
}

func unavailableProxyEndpoint(root, profile string) string {
	return root + "/_proxy/client/unavailable/" + profile
}

func (c *Config) bindDiscoveredProxy(root, token string, catalog *proxyDiscoveryCatalog, file *fileSettings) ([]proxyclient.Binding, error) {
	selected, err := c.proxyProviderSelection(catalog)
	if err != nil {
		return nil, err
	}
	direct, err := c.directProxyExceptions(catalog)
	if err != nil {
		return nil, err
	}
	inference, features, err := selectedProxyRoutes(catalog, selected, direct)
	if err != nil {
		return nil, err
	}
	c.seedDiscoveredProxyProviders(inference, selected, direct)
	served := make(map[proxyDiscoveredDefault]proxyprofile.Profile)
	bindings, err := c.bindProxyInference(root, token, inference, selected, direct, served, file)
	if err != nil {
		return nil, err
	}
	ancillary, err := c.bindProxyFeatures(root, token, features, selected, direct, served)
	if err != nil {
		return nil, err
	}
	classification, err := c.Classify.bindProxy(root, token, c.Proxy.Providers, catalog, direct, served)
	if err != nil {
		return nil, err
	}
	c.resolveProxySlots(file, catalog.Defaults, served)
	return append(append(ancillary, bindings...), classification...), validateProviderModels(c)
}

func selectedProxyRoutes(catalog *proxyDiscoveryCatalog, selected proxySelection, direct map[string]bool) (inference, features map[string]discoveredProxyRoute, err error) {
	inference, features = map[string]discoveredProxyRoute{}, map[string]discoveredProxyRoute{}
	for _, provider := range catalog.Providers {
		if direct[provider.ID] {
			continue
		}
		var routes []proxyDiscoveredRoute
		for _, route := range provider.Routes {
			if selected.includes(provider, route) && !direct[route.Profile] {
				routes = append(routes, route)
			}
		}
		for _, route := range routes {
			r := discoveredProxyRoute{provider: provider, route: route, siblings: routes}
			p, _ := proxyprofile.ByID(route.Profile)
			canonical := p.ID != "" && canonicalProxyProfile(provider.ID, p.ID)
			if canonical && (len(p.Bindings) != 0 || p.Parent != "") {
				if _, exists := features[p.ID]; exists {
					return nil, nil, fmt.Errorf("proxy discovery advertises multiple canonical routes for profile %q", p.ID)
				}
				features[p.ID] = r
			}
			if provider.Family != "inference" || (route.Profile != "" && !proxyprofile.InferenceCapable(p)) {
				continue
			}
			name := provider.ID
			if canonical && p.Parent != "" {
				name = p.ID // e.g. grok-bin in the grok provider
			}
			if direct[name] {
				continue
			}
			if _, exists := inference[name]; exists {
				return nil, nil, fmt.Errorf("proxy discovery advertises multiple inference routes for %q", name)
			}
			inference[name] = r
		}
	}
	return inference, features, nil
}

func (c *Config) seedDiscoveredProxyProviders(inference map[string]discoveredProxyRoute, selected proxySelection, direct map[string]bool) {
	if c.Providers == nil {
		c.Providers = map[string]ProviderConfig{}
	}
	for name := range inference {
		if _, ok := c.Providers[name]; !ok {
			c.Providers[name] = ProviderConfig{FromDefaults: true}
		}
	}
	for _, p := range proxyprofile.All() {
		_, builtin := BuiltinProvider(p.ID)
		// Mistral has a canonical inference profile but no built-in provider type.
		canonical := builtin || p.Provider == ""
		if !proxyprofile.InferenceCapable(p) || direct[p.ID] || !(selected.profiles[p.ID] || selected.all && canonical) {
			continue
		}
		if _, ok := c.Providers[p.ID]; !ok {
			c.Providers[p.ID] = ProviderConfig{Type: p.Provider, FromDefaults: true}
		}
	}
}

func (c *Config) bindProxyInference(root, token string, inference map[string]discoveredProxyRoute, selected proxySelection, direct map[string]bool, served map[proxyDiscoveredDefault]proxyprofile.Profile, file *fileSettings) ([]proxyclient.Binding, error) {
	var bindings []proxyclient.Binding
	for name, pc := range c.Providers {
		r, advertised := inference[name]
		if direct[name] || (!advertised && !selected.all && !selected.profiles[name]) || pc.Proxy != nil || (pc.Enabled != nil && !*pc.Enabled) {
			continue
		}
		p, hints := inferenceProxyProfile(name, pc, r)
		if p.ID == "" || direct[p.ID] {
			continue
		}
		if !advertised && isReservedProxyRouteID(name) {
			return nil, fmt.Errorf("proxy provider %q shadows a reserved integration profile", name)
		}
		endpoint := unavailableProxyEndpoint(root, p.ID)
		if advertised {
			endpoint = r.endpoint(root)
		}
		pc, binding, err := bindProxyProvider(name, pc, p, endpoint, token, hints, file)
		if err != nil {
			return nil, err
		}
		pc.proxyRouteMissing = !advertised
		c.Providers[name] = pc
		if binding.Root == "" {
			continue
		}
		if advertised && canonicalProxyProfile(r.provider.ID, r.route.Profile) {
			served[r.identity()] = p
		}
		related, siblings := r.relatedProxyBindings(root, token)
		binding.RelatedRoots = related
		bindings = append(bindings, siblings...)
		if pc.Type == ProviderTypeOpenCodeGo {
			bindings = append(bindings, openCodeProxyBindings(&binding)...)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func inferenceProxyProfile(name string, pc ProviderConfig, r discoveredProxyRoute) (proxyprofile.Profile, *proxyprofile.ClientProviderConfig) {
	p, found := proxyprofile.ByID(r.route.Profile)
	hints := r.provider.Config
	if found {
		// Provider hints describe its primary inference route, not another
		// built-in implementation that happens to share the same credential.
		if p.Parent != "" && name != r.provider.ID {
			hints = nil
		}
		return p, hints
	}
	if p, ok := proxyprofile.ByID(name); r.route.Name == "" && ok && proxyprofile.InferenceCapable(p) && p.Provider == "" {
		return p, nil // Mistral binds an OpenAI-compatible inference endpoint.
	}
	kind := InferProviderType(name, pc.Type)
	if hints != nil && hints.Type != "" {
		kind = hints.Type
	}
	p, _ = proxyprofile.ForProvider(kind)
	return p, hints
}

// Register granted companion endpoints even for named providers, which may
// use them for account usage without changing any canonical feature settings.
func (r discoveredProxyRoute) relatedProxyBindings(root, token string) (map[string]string, []proxyclient.Binding) {
	related := make(map[string]string)
	var bindings []proxyclient.Binding
	for _, sibling := range r.siblings {
		p, ok := proxyprofile.ByID(sibling.Profile)
		if !ok || sibling.Name == r.route.Name || p.Gate != "" || proxyprofile.InferenceCapable(p) {
			continue
		}
		endpoint := root + strings.TrimRight(sibling.LocalPrefix, "/")
		related[p.ID] = endpoint
		bindings = append(bindings, proxyclient.Binding{Root: endpoint, Token: token, Kind: p.Account})
	}
	return related, bindings
}

func openCodeProxyBindings(b *proxyclient.Binding) []proxyclient.Binding {
	var bindings []proxyclient.Binding
	for _, protocol := range []string{"messages", "gemini"} {
		profile := "opencode-go-" + protocol
		if b.RelatedRoots[profile] != "" {
			continue
		}
		// OpenCode Go's consolidated route serves these two protocol subpaths
		// itself. They are under the granted route, not guesses at sibling routes.
		endpoint := b.Root + "/" + protocol
		b.RelatedRoots[profile] = endpoint
		bindings = append(bindings, proxyclient.Binding{Root: endpoint, Token: b.Token, Kind: b.Kind})
	}
	return bindings
}

func (c *Config) bindProxyFeatures(root, token string, features map[string]discoveredProxyRoute, selected proxySelection, direct map[string]bool, served map[proxyDiscoveredDefault]proxyprofile.Profile) ([]proxyclient.Binding, error) {
	var bindings []proxyclient.Binding
	for _, p := range proxyprofile.All() {
		if direct[p.ID] || p.Gate != "" || !(selected.all || selected.profiles[p.ID]) {
			continue
		}
		if len(p.Bindings) == 0 || !hasProxyFeatureBindings(p) {
			continue
		}
		r, advertised := features[p.ID]
		if !advertised && selected.all && c.Search.useKeylessProxyFallback(p.ID) {
			continue
		}
		endpoint := unavailableProxyEndpoint(root, p.ID)
		if advertised {
			endpoint = r.endpoint(root)
			served[r.identity()] = p
		}
		if err := c.bindProxyFeatureFields(p, endpoint+p.PathSuffix, token); err != nil {
			return nil, err
		}
		bindings = append(bindings, proxyclient.Binding{Root: endpoint, Token: token, Kind: p.Account})
	}
	return bindings, nil
}

func hasProxyFeatureBindings(p proxyprofile.Profile) bool {
	for _, b := range p.Bindings {
		if !strings.HasPrefix(b.Root, "providers.") {
			return true
		}
	}
	return false
}

func (c *Config) bindProxyFeatureFields(profile proxyprofile.Profile, endpoint, token string) error {
	for _, b := range profile.Bindings {
		if strings.HasPrefix(b.Root, "providers.") {
			continue
		}
		for path, value := range map[string]string{b.Root: endpoint, b.Key: token} {
			if path == "" {
				continue
			}
			field, ok := proxyStringField(reflect.ValueOf(c).Elem(), strings.Split(path, "."))
			if !ok {
				return fmt.Errorf("internal proxy binding has unknown config path %s", path)
			}
			field.SetString(value)
		}
	}
	return nil
}

func (c *SearchConfig) useKeylessProxyFallback(id string) bool {
	switch id {
	case "exa-mcp":
		c.suppressExaMCPKey = true
	case "youcom":
		c.suppressYouComKey = true
	case "keenable":
		c.suppressKeenableKey = true
	case "jina-reader":
	default:
		return false
	}
	return true
}
