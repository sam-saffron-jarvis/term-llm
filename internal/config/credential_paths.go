package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// GetConfigDir returns the XDG config directory for term-llm.
// Uses $XDG_CONFIG_HOME if set, otherwise ~/.config.
func GetConfigDir() (string, error) {
	if xdgHome := os.Getenv("XDG_CONFIG_HOME"); xdgHome != "" {
		return filepath.Join(xdgHome, "term-llm"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "term-llm"), nil
}

// GetConfigPath returns the path where config writes are stored.
func GetConfigPath() (string, error) {
	dir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// NativeCredentialsDir selects env > YAML > default without loading providers.
func NativeCredentialsDir() (string, error) {
	value := os.Getenv("TERM_LLM_CREDENTIALS_DIR")
	if value == "" {
		var err error
		value, err = ReadCredentialsDir()
		if err != nil {
			return "", err
		}
	}
	if value == "" {
		return GetConfigDir()
	}
	return resolveCredentialsDir(value)
}

// ReadCredentialsDir reads literal YAML, suppressing parser diagnostics that could expose secrets.
func ReadCredentialsDir() (string, error) {
	path, err := FindConfigFile()
	if err != nil {
		return "", fmt.Errorf("locate credentials_dir config: %w", err)
	}
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read credentials_dir config: %w", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(data, &values); err != nil {
		return "", fmt.Errorf("read credentials_dir: invalid YAML configuration")
	}
	return credentialsDirSetting(values)
}

func credentialsDirSetting(values map[string]any) (string, error) {
	var value any
	found := false
	for key, candidate := range values {
		if !strings.EqualFold(key, "credentials_dir") {
			continue
		}
		if found {
			return "", fmt.Errorf("credentials_dir has duplicate case-variant keys")
		}
		value, found = candidate, true
	}
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("credentials_dir must be a directory-path string")
	}
	return text, nil
}

func resolveCredentialsDir(value string) (string, error) {
	home := ""
	if value == "~" || strings.HasPrefix(value, "~/") {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand credentials_dir / TERM_LLM_CREDENTIALS_DIR home: %w", err)
		}
	}
	return ResolveCredentialsDir(value, home)
}

// ValidateCredentialsDirEdit validates credentials_dir config edits.
func ValidateCredentialsDirEdit(key, value string) error {
	if !strings.EqualFold(key, "credentials_dir") || value == "" {
		return nil
	}
	_, err := resolveCredentialsDir(value)
	return err
}

// ResolveCredentialsDir validates paths without creating directories.
func ResolveCredentialsDir(dir, home string) (string, error) {
	const setting = "credentials_dir / TERM_LLM_CREDENTIALS_DIR"
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("%s requires an absolute HOME for home expansion", setting)
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
		return "", fmt.Errorf("%s must be an absolute directory path (or ~/path)", setting)
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("stat %s: %w", setting, err)
	}
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory", setting)
	}
	return dir, nil
}

// prepareCredentialsDirSetting skips invalid unused YAML under an env override.
func prepareCredentialsDirSetting(settings map[string]any) error {
	value, err := credentialsDirSetting(settings)
	override := os.Getenv("TERM_LLM_CREDENTIALS_DIR")
	if err != nil && override == "" {
		return err
	}
	settings["credentials_dir"] = value
	if override != "" {
		value = override
	}
	if value != "" {
		_, err = resolveCredentialsDir(value)
	}
	return err
}
