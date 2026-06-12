package hub

import (
	"net"

	"github.com/samsaffron/term-llm/internal/contain"
)

// ContainResolver discovers nodes from local contain workspaces: each
// workspace with a provisioned web token becomes a node whose URL/token come
// from its .env (via contain.ReadWebConfig). List and Read are fields so
// tests can substitute fakes for the container config directory.
type ContainResolver struct {
	// Host is the host the workspaces' serves are published on (loopback in
	// the one-container-per-agent shape).
	Host string
	// List enumerates contain workspace definitions.
	List func() ([]contain.ListEntry, error)
	// Read resolves a workspace's web config from its .env.
	Read func(name string) (contain.WebConfig, error)
}

// NewContainResolver returns a resolver over the local contain workspaces.
func NewContainResolver() *ContainResolver {
	return &ContainResolver{
		Host: "127.0.0.1",
		List: contain.Definitions,
		Read: contain.ReadWebConfig,
	}
}

// Source implements Resolver.
func (c *ContainResolver) Source() string { return SourceContain }

// Nodes implements Resolver. Workspaces whose .env cannot be read or that
// have no provisioned web token are skipped (they have no reachable serve to
// front), so a half-created workspace never breaks hub discovery.
func (c *ContainResolver) Nodes() ([]Node, error) {
	entries, err := c.List()
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(entries))
	for _, e := range entries {
		web, err := c.Read(e.Name)
		if err != nil || web.Token == "" {
			continue
		}
		if ValidateID(e.Name) != nil {
			continue
		}
		nodes = append(nodes, Node{
			ID:       e.Name,
			Name:     e.Name,
			Source:   SourceContain,
			URL:      "http://" + net.JoinHostPort(c.Host, web.Port),
			BasePath: web.BasePath,
			Token:    web.Token,
		})
	}
	return nodes, nil
}
