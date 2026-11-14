package service

import "github.com/term-llm/term-llm/internal/credentials"

// normalizeServiceCredentialsDir captures a literal directory, not a secret or
// shell expression. Expand against the HOME recorded for the runner rather than
// the installing process's HOME. Keep unset/empty values so runtime XDG defaults
// remain defaults, and never create a directory while installing a service.
func normalizeServiceCredentialsDir(env map[string]string) error {
	const key = "TERM_LLM_CREDENTIALS_DIR"
	if env[key] == "" {
		return nil
	}
	dir, err := credentials.ResolveDir(env[key], env["HOME"])
	if err != nil {
		return err
	}
	env[key] = dir
	return nil
}
