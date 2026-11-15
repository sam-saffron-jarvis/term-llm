package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/term-llm/term-llm/internal/fileprivacy"
	"golang.org/x/net/http/httpguts"
	"gopkg.in/yaml.v3"
)

const maxConfig = 1 << 20

var slug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type Auth struct {
	Header string `json:"header" yaml:"header"`
	Prefix string `json:"prefix" yaml:"prefix,omitempty"`
}
type HeaderRule struct {
	Secret string  `json:"secret,omitempty" yaml:"secret,omitempty"`
	Prefix string  `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Value  *string `json:"value,omitempty" yaml:"value,omitempty"`
}
type Route struct {
	Name            string                `json:"name" yaml:"name"`
	LocalPrefix     string                `json:"local_prefix" yaml:"local_prefix"`
	Upstream        string                `json:"upstream" yaml:"upstream"`
	Enabled         bool                  `json:"enabled" yaml:"enabled"`
	AllowHTTP       bool                  `json:"allow_insecure_http" yaml:"allow_insecure_http,omitempty"`
	CallerAuth      Auth                  `json:"caller_auth" yaml:"caller_auth"`
	SetHeaders      map[string]HeaderRule `json:"set_headers" yaml:"set_headers"`
	RequestIDHeader string                `json:"request_id_header,omitempty" yaml:"request_id_header,omitempty"`
}
type SecretRef struct {
	Origin string `json:"origin,omitempty" yaml:"origin,omitempty"`
	File   string `json:"file,omitempty" yaml:"file,omitempty"`
	Stored string `json:"stored,omitempty" yaml:"stored,omitempty"`
}
type Config struct {
	Routes  []Route              `json:"routes" yaml:"routes"`
	Secrets map[string]SecretRef `json:"secrets" yaml:"secrets,omitempty"`
}
type material struct{ Origin, Value string }
type activeRoute struct {
	Route
	target    *url.URL
	origin    string
	authority string
	headers   http.Header
}
type snapshot struct {
	config   Config
	revision int64
	routes   []activeRoute
	secrets  map[string]material
}

// Origin normalizes the credential binding, including the effective port.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("upstream must be an HTTP(S) URL without userinfo, query or fragment")
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("upstream port must be between 1 and 65535")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		// Canonicalize IP spelling without lowercasing case-sensitive IPv6 zones.
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	return (&url.URL{Scheme: u.Scheme, Host: net.JoinHostPort(host, strconv.Itoa(n))}).String(), nil
}

func secretOrigin(raw string) (string, error) {
	origin, err := Origin(raw)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(raw)
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("secret binding must be an origin without a base path")
	}
	return origin, nil
}
func safePath(escaped string) bool {
	if len(escaped) > 8192 || !strings.HasPrefix(escaped, "/") {
		return false
	}
	// Reject multiply encoded traversal too; retain ordinary escaped identifiers.
	p := escaped
	for i := 0; i < 4; i++ {
		for _, c := range p {
			if c == '\\' || c < 32 || c == 127 {
				return false
			}
		}
		for _, s := range strings.Split(p, "/") {
			if s == "." || s == ".." {
				return false
			}
		}
		q, err := url.PathUnescape(p)
		if err != nil {
			// A decoded literal percent is an identifier, not malformed wire escaping.
			return i > 0
		}
		if q == p {
			return true
		}
		p = q
	}
	return false
}
func forbiddenHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "content-length", "forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto", "x-real-ip", "x-original-url", "x-rewrite-url", "cookie":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "x-term-llm-proxy-")
}
func validHeader(h string) bool { return httpguts.ValidHeaderFieldName(h) && !forbiddenHeader(h) }
func decodeConfig(data []byte) (Config, error) {
	var c Config
	if len(data) > maxConfig {
		return c, fmt.Errorf("configuration exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid YAML configuration (check fields and types)")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("configuration must contain one document")
	}
	// Omitted enabled means enabled in operator/import YAML; JSON updates are explicit.
	var root yaml.Node
	if yaml.Unmarshal(data, &root) == nil && len(root.Content) > 0 {
		n := root.Content[0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "routes" {
				for j, item := range n.Content[i+1].Content {
					found := false
					for k := 0; k+1 < len(item.Content); k += 2 {
						found = found || item.Content[k].Value == "enabled"
					}
					if !found && j < len(c.Routes) {
						c.Routes[j].Enabled = true
					}
				}
			}
		}
	}
	return c, nil
}
func privateFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open private credential file")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || fileprivacy.CheckFile(st) != nil || st.Size() > limit {
		return nil, fmt.Errorf("credential file must be a private regular file within size limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("cannot read credential file")
	}
	return b, nil
}
func fileConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("cannot open operator configuration")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfig+1))
	if err != nil {
		return Config{}, fmt.Errorf("cannot read operator configuration")
	}
	return decodeConfig(b)
}
func resolveSecrets(c Config, stored map[string]material, adminFile string, fileMode bool, protected ...string) (map[string]material, error) {
	out := make(map[string]material)
	protected = append(protected, adminFile)
	for name, ref := range c.Secrets {
		if !slug.MatchString(name) {
			return nil, fmt.Errorf("invalid secret name")
		}
		if (ref.File == "") == (ref.Stored == "") {
			return nil, fmt.Errorf("secret %s needs exactly one source", name)
		}
		if ref.Stored != "" && ref.Stored != name {
			return nil, fmt.Errorf("stored secret reference must match its declared name")
		}
		if ref.File != "" {
			origin, err := secretOrigin(ref.Origin)
			if err != nil {
				return nil, fmt.Errorf("file secret requires a valid declared origin")
			}
			if !fileMode {
				return nil, fmt.Errorf("file secret references are operator-only")
			}
			info, e := os.Stat(ref.File)
			if e != nil || forbiddenSecretFile(ref.File, info, protected) {
				return nil, fmt.Errorf("secret file unavailable or forbidden")
			}
			b, e := privateFile(ref.File, 16<<10)
			if e != nil {
				return nil, e
			}
			value := strings.TrimRight(string(b), "\r\n")
			if value == "" {
				return nil, fmt.Errorf("credential file must contain a nonempty secret")
			}
			out[name] = material{Origin: origin, Value: value}
		} else {
			v, ok := stored[ref.Stored]
			if !ok {
				continue
			}
			if ref.Origin != "" {
				origin, err := secretOrigin(ref.Origin)
				if err != nil || origin != v.Origin {
					return nil, fmt.Errorf("stored secret declaration does not match its bound origin")
				}
			}
			out[name] = v
		}
	}
	// UI-managed stored secrets are implicitly declared and never exported as values.
	if !fileMode {
		for name, v := range stored {
			out[name] = v
		}
	}
	return out, nil
}
func compile(c Config, revision int64, secrets map[string]material) (*snapshot, error) {
	if len(c.Routes) > 128 || len(c.Secrets) > 256 {
		return nil, fmt.Errorf("at most 128 routes and 256 secret references")
	}
	if c.Routes == nil {
		c.Routes = []Route{}
	}
	s := &snapshot{config: c, revision: revision, secrets: secrets}
	names := map[string]bool{}
	for i := range s.config.Routes {
		r := &s.config.Routes[i]
		if !slug.MatchString(r.Name) || names[r.Name] {
			return nil, fmt.Errorf("route names must be unique immutable slugs")
		}
		names[r.Name] = true
		if err := validatePrefix(*r, s.routes); err != nil {
			return nil, err
		}
		origin, err := Origin(r.Upstream)
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(r.Upstream)
		if u.Scheme == "http" && !r.AllowHTTP {
			return nil, fmt.Errorf("HTTP upstream requires allow_insecure_http")
		}
		if !safePath("/" + strings.TrimPrefix(u.EscapedPath(), "/")) {
			return nil, fmt.Errorf("upstream base path is not canonical")
		}
		if r.CallerAuth.Header == "" {
			r.CallerAuth = Auth{Header: "Authorization", Prefix: "Bearer "}
		}
		if !validHeader(r.CallerAuth.Header) || !httpguts.ValidHeaderFieldValue(r.CallerAuth.Prefix) {
			return nil, fmt.Errorf("invalid local authentication header")
		}
		if r.RequestIDHeader != "" && !validHeader(r.RequestIDHeader) {
			return nil, fmt.Errorf("invalid request ID header")
		}
		headers, err := compileHeaders(*r, origin, secrets)
		if err != nil {
			return nil, err
		}
		s.routes = append(s.routes, activeRoute{Route: *r, target: u, origin: origin, authority: routeAuthority(*r, origin, u), headers: headers})
	}
	return s, nil
}

func validatePrefix(r Route, routes []activeRoute) error {
	if !safePath(r.LocalPrefix) || r.LocalPrefix == "/" || !strings.HasSuffix(r.LocalPrefix, "/") || strings.Contains(r.LocalPrefix, "%") || strings.Contains(r.LocalPrefix, "//") {
		return fmt.Errorf("route %s needs a canonical non-root prefix ending in /", r.Name)
	}
	for _, reserved := range []string{"/_proxy/", "/healthz/", "/assets/"} {
		if strings.HasPrefix(r.LocalPrefix, reserved) || strings.HasPrefix(reserved, r.LocalPrefix) {
			return fmt.Errorf("route prefix is reserved")
		}
	}
	for _, other := range routes {
		if strings.HasPrefix(r.LocalPrefix, other.LocalPrefix) || strings.HasPrefix(other.LocalPrefix, r.LocalPrefix) {
			return fmt.Errorf("local route prefixes cannot overlap")
		}
	}
	return nil
}

func compileHeaders(r Route, origin string, secrets map[string]material) (http.Header, error) {
	if len(r.SetHeaders) > 32 {
		return nil, fmt.Errorf("at most 32 header substitutions")
	}
	headers := make(http.Header)
	keys := make([]string, 0, len(r.SetHeaders))
	for key := range r.SetHeaders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rule := r.SetHeaders[key]
		if rule.Secret != "" && !slug.MatchString(rule.Secret) {
			return nil, fmt.Errorf("substitution secret reference must be a name of at most 64 letters, numbers, - or _")
		}
		if !validHeader(key) || headers.Values(key) != nil || (rule.Secret == "") == (rule.Value == nil) {
			return nil, fmt.Errorf("header rules need unique valid names and either secret or value")
		}
		value := ""
		if rule.Value != nil {
			if rule.Prefix != "" {
				return nil, fmt.Errorf("literal headers cannot have a prefix")
			}
			value = *rule.Value
		} else {
			v, ok := secrets[rule.Secret]
			if !ok {
				if r.Enabled {
					return nil, fmt.Errorf("header substitution references an unavailable secret; store it first or disable the route")
				}
			} else {
				if v.Origin == "" || v.Origin != origin {
					return nil, fmt.Errorf("substitution secret is not bound to this upstream origin")
				}
				value = v.Value
			}
			value = rule.Prefix + value
		}
		if !httpguts.ValidHeaderFieldValue(value) || len(value) > 16<<10 {
			return nil, fmt.Errorf("invalid substitution header value")
		}
		headers.Set(key, value)
	}
	return headers, nil
}

// routeAuthority identifies the delegated upstream scope, not live key bytes.
// Routine secret rotation and local endpoint/auth-format changes keep grants;
// moving the base or changing an outbound rule requires an explicit regrant.
func routeAuthority(r Route, origin string, u *url.URL) string {
	type rule struct {
		Header, Secret, Prefix string
		Value                  *string
	}
	rules := make([]rule, 0, len(r.SetHeaders))
	for key, v := range r.SetHeaders {
		rules = append(rules, rule{strings.ToLower(key), v.Secret, v.Prefix, v.Value})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Header < rules[j].Header })
	base := strings.TrimSuffix(u.EscapedPath(), "/") + "/"
	// Escape hex digits are case-insensitive, but encoded separators remain encoded.
	encoded := []byte(base)
	for i := 0; i+2 < len(encoded); i++ {
		if encoded[i] == '%' {
			encoded[i+1] = upperHex(encoded[i+1])
			encoded[i+2] = upperHex(encoded[i+2])
			i += 2
		}
	}
	b, _ := json.Marshal(struct {
		Origin, Base string
		Rules        []rule
	}{origin, string(encoded), rules})
	return hashToken(string(b))
}
func upperHex(b byte) byte {
	if b >= 'a' && b <= 'f' {
		return b - 'a' + 'A'
	}
	return b
}
