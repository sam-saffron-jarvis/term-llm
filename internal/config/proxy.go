package config

import (
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

// ProxyConfig opts integrations into a trusted credential proxy. Direct is an
// explicit exception list, not a fallback when the proxy is unavailable.
type ProxyConfig struct {
	URL    string   `mapstructure:"url" yaml:"url,omitempty"`
	Token  string   `mapstructure:"token" yaml:"token,omitempty"`
	Direct []string `mapstructure:"direct" yaml:"direct,omitempty"`
}

var proxyRouteName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func proxyRoot(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("proxy.url must be an HTTPS root without credentials, query or fragment")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("proxy.url requires HTTPS except on loopback")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." || strings.Contains(part, "\\") {
			return "", fmt.Errorf("proxy.url path must be canonical")
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (c *Config) materializeProxy(_ *fileSettings) error {
	if c.Proxy.URL == "" {
		if c.Proxy.Token != "" || len(c.Proxy.Direct) > 0 {
			return fmt.Errorf("proxy.url is required when proxy settings are present")
		}
		return nil
	}
	root, err := proxyRoot(c.Proxy.URL)
	if err != nil {
		return err
	}
	token, err := Cred(c.Proxy.Token).Resolve()
	if err != nil || len(token) < 16 || strings.ContainsAny(token, "\r\n\t ") {
		return fmt.Errorf("proxy.token must resolve to a private scoped client token (at least 16 characters)")
	}
	c.Proxy.URL = root
	if c.Providers == nil {
		c.Providers = map[string]ProviderConfig{}
	}
	direct := map[string]bool{}
	known := map[string]bool{}
	for _, p := range proxyprofile.All() {
		known[p.ID] = true
		if p.Provider != "" {
			known[string(p.Provider)] = true
		}
	}
	for name := range c.Providers {
		known[name] = true
	}
	for _, name := range c.Proxy.Direct {
		if !known[name] || direct[name] {
			return fmt.Errorf("proxy.direct must contain unique known integration names")
		}
		direct[name] = true
	}
	// Dynamic OpenCode Go uses one credential across its protocol siblings;
	// a partial direct exception cannot safely reuse the scoped proxy token.
	for _, p := range proxyprofile.All() {
		if strings.HasPrefix(p.ID, "opencode-go-") && direct[p.ID] && !direct["opencode-go"] {
			return fmt.Errorf("proxy.direct must select opencode-go as a whole, not only %s", p.ID)
		}
	}
	bindings := []proxyclient.Binding{}
	for _, spec := range BuiltinProviders() {
		if _, ok := c.Providers[spec.Name]; !ok {
			c.Providers[spec.Name] = ProviderConfig{Type: spec.Type, FromDefaults: true}
		}
	}
	for name, pc := range c.Providers {
		kind := InferProviderType(name, pc.Type)
		p, ok := proxyprofile.ForProvider(kind)
		if !ok || direct[name] || direct[p.ID] || direct[string(kind)] || pc.Enabled != nil && !*pc.Enabled {
			continue
		}
		gate := p.Gate
		if (kind == ProviderTypeOpenAICompat || kind == ProviderTypeVLLM) && strings.TrimSpace(pc.URL) != "" {
			// A complete chat URL is not an API root. Replacing it with a base
			// would silently append /chat/completions and change its operation.
			gate = "exact url endpoints need a compatible base_url and an explicit trusted upstream route; proxy mode cannot infer an exact endpoint mapping"
		}
		if gate != "" {
			pc.Type = kind
			pc.FromDefaults = false
			pc.ProxyBinding = "gated:" + gate
			pc.APIKey, pc.ResolvedAPIKey = "", ""
			pc.Env = nil
			c.Providers[name] = pc
			continue
		}
		id := p.ID
		if _, builtInName := BuiltinProvider(name); !builtInName || kind == ProviderTypeOpenAICompat || kind == ProviderTypeVLLM {
			id = name
		}
		if !proxyRouteName.MatchString(id) {
			return fmt.Errorf("proxy provider name must be a route slug")
		}
		pc.Type = kind
		pc.BaseURL = root + "/" + id
		pc.URL = ""
		pc.ResolvedURL = ""
		pc.ProxyBinding = pc.BaseURL
		pc.FromDefaults = false
		pc.APIKey = token
		pc.ResolvedAPIKey = token
		pc.Headers = nil
		pc.Env = nil
		if p.CLIRootEnv != "" {
			pc.Env = map[string]string{p.CLIRootEnv: pc.BaseURL}
		}
		pc.credentialsResolved = true
		pc.needsLazyResolution = false
		pc.AccessKey = ""
		pc.SecretKey = ""
		pc.SessionToken = ""
		pc.Profile = ""
		if p.Account != "" {
			pc.APIKey = ""
			pc.ResolvedAPIKey = ""
		}
		if kind == ProviderTypeAnthropic {
			pc.Credentials = "api_key"
		}
		c.Providers[name] = pc
		bindings = append(bindings, proxyclient.Binding{Root: pc.BaseURL, Token: token, Kind: p.Account})
		if kind == ProviderTypeOpenCodeGo {
			bindings = append(bindings, proxyclient.Binding{Root: root + "/" + id + "-messages", Token: token}, proxyclient.Binding{Root: root + "/" + id + "-gemini", Token: token})
		}
	}
	for _, p := range proxyprofile.All() {
		familyDirect := strings.HasPrefix(p.ID, "opencode-go-") && direct["opencode-go"]
		if direct[p.ID] || familyDirect || p.Gate != "" || p.Provider != "" && direct[string(p.Provider)] {
			continue
		}
		// Standalone ancillary/protocol routes also receive an explicit binding;
		// no missing sibling may fall back to the public vendor endpoint.
		bindings = append(bindings, proxyclient.Binding{Root: root + "/" + p.ID, Token: token, Kind: p.Account})
		for _, b := range p.Bindings {
			if strings.HasPrefix(b.Root, "providers.") {
				continue
			} // named transcription providers use the provider loop above
			field, ok := proxyStringField(reflect.ValueOf(c).Elem(), strings.Split(b.Root, "."))
			if !ok {
				return fmt.Errorf("internal proxy binding has unknown config path %s", b.Root)
			}
			endpoint := root + "/" + p.ID + p.PathSuffix
			field.SetString(endpoint)
			if b.Key != "" {
				key, ok := proxyStringField(reflect.ValueOf(c).Elem(), strings.Split(b.Key, "."))
				if !ok {
					return fmt.Errorf("internal proxy credential binding has unknown path %s", b.Key)
				}
				key.SetString(token)
			}
			bindings = append(bindings, proxyclient.Binding{Root: endpoint, Token: token, Kind: p.Account})
		}
	}
	return proxyclient.Register(bindings)
}

func proxyStringField(value reflect.Value, path []string) (reflect.Value, bool) {
	if value.Kind() != reflect.Struct || len(path) == 0 {
		return reflect.Value{}, false
	}
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("mapstructure"), ",")[0]
		if name != path[0] {
			continue
		}
		field := value.Field(i)
		if len(path) == 1 {
			return field, field.Kind() == reflect.String && field.CanSet()
		}
		return proxyStringField(field, path[1:])
	}
	return reflect.Value{}, false
}

func (p *ProviderConfig) resolveProxyCredentials(name string) error {
	if strings.HasPrefix(p.ProxyBinding, "gated:") {
		return fmt.Errorf("provider %q cannot run credentiallessly through the proxy yet: %s; explicitly list it in proxy.direct only if local credential custody is intended", name, strings.TrimPrefix(p.ProxyBinding, "gated:"))
	}
	p.credentialsResolved = true
	p.needsLazyResolution = false
	return nil
}

// inferenceReadyError guards runtime delegation before the ordinary lazy-value
// fast path. Factory callers must not bypass a fail-closed transport gate.
func (p *ProviderConfig) inferenceReadyError() error {
	if p.ProxyBinding != "" {
		return p.resolveProxyCredentials(string(p.Type))
	}
	return nil
}
