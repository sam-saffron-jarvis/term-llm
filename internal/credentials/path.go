package credentials

import (
	"path/filepath"

	"github.com/term-llm/term-llm/internal/config"
)

// Dir selects native OAuth storage: environment override, YAML, then config directory.
func Dir() (string, error) {
	return config.NativeCredentialsDir()
}

func credentialPath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
