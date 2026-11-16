package credentials

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

const delegatedExpiry = int64(4102444800) // 2100-01-01; the trusted proxy owns vendor expiry.

func delegatedIdentity(root, token string) string {
	sum := sha256.Sum256([]byte(root + "\x00" + token))
	return "proxy-" + hex.EncodeToString(sum[:12])
}

// GetChatGPTCredentialsForEndpoint resolves an explicitly delegated API root
// before considering local OAuth files. A custom/direct root keeps local auth.
func GetChatGPTCredentialsForEndpoint(endpoint string) (*ChatGPTCredentials, error) {
	if b, ok := proxyclient.Lookup(endpoint); ok && b.Kind == "chatgpt" {
		return &ChatGPTCredentials{AccessToken: b.Token, AccountID: delegatedIdentity(b.Root, b.Token), ExpiresAt: delegatedExpiry, delegated: true}, nil
	}
	return GetChatGPTCredentials()
}

// GetGrokCredentialsForEndpoint never opens the native store for a delegated root.
func GetGrokCredentialsForEndpoint(endpoint string) (*GrokCredentials, error) {
	if b, ok := proxyclient.Lookup(endpoint); ok && b.Kind == "grok" {
		return &GrokCredentials{AccessToken: b.Token, AccountID: delegatedIdentity(b.Root, b.Token), ExpiresAt: delegatedExpiry, delegated: true}, nil
	}
	return GetGrokCredentials()
}

// GetCopilotCredentialsForEndpoint returns a scoped stand-in for a proxy-owned
// Copilot session. It is not a GitHub token and must never be exchanged.
func GetCopilotCredentialsForEndpoint(endpoint string) (*CopilotCredentials, error) {
	if b, ok := proxyclient.Lookup(endpoint); ok && b.Kind == "copilot" {
		return &CopilotCredentials{AccessToken: b.Token, ExpiresAt: delegatedExpiry, delegated: true}, nil
	}
	return GetCopilotCredentials()
}

func (c *ChatGPTCredentials) Delegated() bool { return c != nil && c.delegated }
func (c *GrokCredentials) Delegated() bool    { return c != nil && c.delegated }
func (c *CopilotCredentials) Delegated() bool { return c != nil && c.delegated }
