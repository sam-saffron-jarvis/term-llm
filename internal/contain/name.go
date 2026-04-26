package contain

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var validNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidateName validates a contain workspace name. Names are intentionally
// limited to simple path-safe characters because they are used as directory
// names below the global containers root.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("contain workspace name must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid contain workspace name %q", name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("contain workspace name %q must not contain path separators", name)
	}
	if runtime.GOOS == "windows" && strings.ContainsRune(name, ':') {
		return fmt.Errorf("contain workspace name %q must not contain path separators", name)
	}
	if !validNamePattern.MatchString(name) {
		return fmt.Errorf("contain workspace name %q may only contain letters, digits, '.', '_' and '-'", name)
	}
	return nil
}

// SafeName returns the deterministic name fragment used for Docker Compose
// project names and Docker resource names. It is tolerant so callers can use it
// in error paths, but valid workspace names should be checked with ValidateName.
func SafeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == '.' {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	safe := strings.Trim(b.String(), "-")
	if safe == "" {
		return "workspace"
	}
	return safe
}

// ProjectName returns the deterministic Docker Compose project name for a
// contain workspace.
func ProjectName(name string) string {
	return "term-llm-contain-" + SafeName(name)
}
