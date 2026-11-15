package proxy

import (
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) resolveSecrets(c Config, stored map[string]material, fileMode bool) (map[string]material, error) {
	protected := []string{s.opts.PasskeyBootstrapTokenFile, s.opts.PasskeyRecoveryTokenFile}
	if s.passkey != nil {
		dir := filepath.Join(s.opts.StateDir, "admin-auth")
		protected = append(protected, dir, filepath.Join(dir, "credentials.json"), filepath.Join(dir, "auth.lock"))
	}
	return resolveSecrets(c, stored, s.opts.AdminTokenFile, fileMode, protected...)
}

// Protect both path spellings and inode aliases. Directory protection includes
// symlinked directory aliases; ordinary operator file secrets remain supported.
func forbiddenSecretFile(path string, info os.FileInfo, protected []string) bool {
	abs, _ := filepath.Abs(path)
	resolved, _ := filepath.EvalSymlinks(abs)
	for _, candidate := range protected {
		if candidate == "" {
			continue
		}
		other, _ := filepath.Abs(candidate)
		if abs == other {
			return true
		}
		st, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if os.SameFile(st, info) {
			return true
		}
		if st.IsDir() {
			dir, _ := filepath.EvalSymlinks(other)
			if dir != "" && (resolved == dir || strings.HasPrefix(resolved, dir+string(filepath.Separator))) {
				return true
			}
		}
	}
	return false
}
