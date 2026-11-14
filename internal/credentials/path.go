package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Dir returns the directory for term-llm-managed provider OAuth stores.
// A nonempty TERM_LLM_CREDENTIALS_DIR overrides the default config directory.
// An explicit override never falls back to the default credential directory.
func Dir() (string, error) {
	if dir := os.Getenv("TERM_LLM_CREDENTIALS_DIR"); dir != "" {
		home := ""
		if dir == "~" || strings.HasPrefix(dir, "~/") {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("expand TERM_LLM_CREDENTIALS_DIR home: %w", err)
			}
		}
		return ResolveDir(dir, home)
	}

	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "term-llm"), nil
}

// ResolveDir validates a nonempty TERM_LLM_CREDENTIALS_DIR override, expanding
// only ~ or ~/ against home. Service installers pass the recorded service HOME
// so their path policy matches runtime without depending on the installer's HOME.
// No environment or secret substitutions are performed. Missing directories are
// allowed and are created only by credential mutations, not by path resolution.
func ResolveDir(dir, home string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("TERM_LLM_CREDENTIALS_DIR requires an absolute HOME for home expansion")
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
		return "", fmt.Errorf("TERM_LLM_CREDENTIALS_DIR must be an absolute directory path (or ~/path)")
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat TERM_LLM_CREDENTIALS_DIR: %w", err)
	}
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("TERM_LLM_CREDENTIALS_DIR must be a directory")
	}
	return dir, nil
}

func credentialPath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
