package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GeminiOAuthCredentials holds the OAuth credentials loaded from ~/.gemini/oauth_creds.json
type GeminiOAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiryDate   int64  `json:"expiry_date"`
}

// GetGeminiOAuthCredentials loads OAuth credentials from ~/.gemini/oauth_creds.json
// These are used for the Code Assist API (cloudcode-pa.googleapis.com)
func GetGeminiOAuthCredentials() (*GeminiOAuthCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	credPath := filepath.Join(home, ".gemini", "oauth_creds.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("gemini-cli OAuth credentials not found.\n" +
				"Please run 'gemini' to authenticate first, or use GEMINI_API_KEY instead")
		}
		return nil, fmt.Errorf("failed to read credentials: %w", err)
	}

	var creds GeminiOAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("invalid credentials: missing refresh_token")
	}

	return &creds, nil
}

// GetGeminiCredentials returns an error explaining how to configure Gemini.
// Kept for backwards compatibility with the old error message.
func GetGeminiCredentials() (string, error) {
	return "", fmt.Errorf("gemini credentials require configuration.\n" +
		"Options:\n" +
		"  1. Set GEMINI_API_KEY for consumer Gemini API\n" +
		"  2. Set credentials: gemini-cli to use gemini-cli OAuth (Code Assist API)")
}
