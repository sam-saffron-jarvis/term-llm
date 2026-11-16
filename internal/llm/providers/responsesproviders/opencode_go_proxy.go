package responsesproviders

import (
	"fmt"
	"strings"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

// A scoped OpenCode Go client has three least-privilege routes: its ordinary
// Chat/Responses root, Messages with x-api-key, and the separate Gemini API
// root with x-goog-api-key. No generic body rewrite or alternate caller auth is
// needed, and custom named accounts keep their own sibling route namespace.
func openCodeGoProtocolRoots(baseURL string) (messages, gemini string, err error) {
	source, bound := proxyclient.Lookup(baseURL)
	if !bound {
		return baseURL, baseURL, nil
	}
	i := strings.LastIndex(source.Root, "/")
	if i < 0 {
		return "", "", fmt.Errorf("invalid OpenCode Go proxy route")
	}
	id := source.Root[i+1:]
	m, ok := proxyclient.Related(baseURL, id+"-messages")
	if !ok {
		return "", "", fmt.Errorf("OpenCode Go Messages proxy route is missing; no direct fallback")
	}
	g, ok := proxyclient.Related(baseURL, id+"-gemini")
	if !ok {
		return "", "", fmt.Errorf("OpenCode Go Gemini proxy route is missing; no direct fallback")
	}
	return m.Root, g.Root, nil
}
