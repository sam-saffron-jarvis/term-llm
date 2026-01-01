package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type claudeCredentials struct {
	ClaudeAiOauth *oauthCredentials `json:"claudeAiOauth"`
}

type oauthCredentials struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"`
}

// GetClaudeToken retrieves the Anthropic API token from Claude Code credentials.
// On macOS, it reads from the system keychain.
// On other platforms, it reads from ~/.claude/.credentials.json
func GetClaudeToken() (string, error) {
	var jsonData []byte
	var err error

	if runtime.GOOS == "darwin" {
		jsonData, err = getFromMacKeychain()
	} else {
		jsonData, err = getFromCredentialsFile()
	}

	if err != nil {
		return "", err
	}

	var creds claudeCredentials
	if err := json.Unmarshal(jsonData, &creds); err != nil {
		return "", fmt.Errorf("failed to parse claude credentials: %w", err)
	}

	if creds.ClaudeAiOauth == nil || creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("no access token found in claude credentials")
	}

	return creds.ClaudeAiOauth.AccessToken, nil
}

func getFromMacKeychain() ([]byte, error) {
	user := os.Getenv("USER")
	if user == "" {
		return nil, fmt.Errorf("USER environment variable not set")
	}

	cmd := exec.Command("security", "find-generic-password",
		"-s", "Claude Code-credentials",
		"-a", user,
		"-w")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read from keychain: %w (is Claude Code installed and logged in?)", err)
	}

	return output, nil
}

func getFromCredentialsFile() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w (is Claude Code installed and logged in?)", credPath, err)
	}

	return data, nil
}
