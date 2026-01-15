package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// codexAuth matches the structure of ~/.codex/auth.json
type codexAuth struct {
	OpenAIAPIKey *string      `json:"OPENAI_API_KEY,omitempty"`
	Tokens       *codexTokens `json:"tokens,omitempty"`
}

type codexTokens struct {
	AccessToken string  `json:"access_token"`
	AccountID   *string `json:"account_id,omitempty"`
}

// CodexCredentials holds the token and optional account ID for Codex auth
type CodexCredentials struct {
	AccessToken string
	AccountID   string // Empty if not available
}

// GetCodexCredentials retrieves the OpenAI credentials from Codex auth.
// It reads from ~/.codex/auth.json and returns the OAuth access_token
// along with the account_id (needed for ChatGPT backend API).
// If tokens are expired, run 'codex login' to refresh them.
func GetCodexCredentials() (*CodexCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	authPath := filepath.Join(home, ".codex", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w (run 'codex login')", authPath, err)
	}

	var auth codexAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, fmt.Errorf("failed to parse codex credentials: %w", err)
	}

	// Prefer OAuth access token (uses ChatGPT backend)
	if auth.Tokens != nil && auth.Tokens.AccessToken != "" {
		creds := &CodexCredentials{
			AccessToken: auth.Tokens.AccessToken,
		}
		if auth.Tokens.AccountID != nil {
			creds.AccountID = *auth.Tokens.AccountID
		}
		return creds, nil
	}

	// Fall back to API key stored in Codex config (uses api.openai.com)
	if auth.OpenAIAPIKey != nil && *auth.OpenAIAPIKey != "" {
		return &CodexCredentials{
			AccessToken: *auth.OpenAIAPIKey,
			AccountID:   "", // No account ID for API key auth
		}, nil
	}

	return nil, fmt.Errorf("no credentials in %s (run 'codex login')", authPath)
}

// GetCodexToken is a convenience wrapper that returns just the token.
// Deprecated: Use GetCodexCredentials instead to get both token and account ID.
func GetCodexToken() (string, error) {
	creds, err := GetCodexCredentials()
	if err != nil {
		return "", err
	}
	return creds.AccessToken, nil
}
