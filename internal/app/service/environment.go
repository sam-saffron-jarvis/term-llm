package service

import "github.com/term-llm/term-llm/internal/config"

// normalizeServiceCredentialsDir captures only explicit env overrides using the service HOME.
func normalizeServiceCredentialsDir(env map[string]string) error {
	const key = "TERM_LLM_CREDENTIALS_DIR"
	if env[key] == "" {
		return nil
	}
	dir, err := config.ResolveCredentialsDir(env[key], env["HOME"])
	if err != nil {
		return err
	}
	env[key] = dir
	return nil
}
