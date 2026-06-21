package cmd

import (
	"net/http"
	"strings"
)

// hubPath maps an internal hub route ("/api/nodes", "/node/id/") to the
// externally mounted route. A root-mounted hub keeps paths unchanged; a hub
// served with --base-path /hub emits "/hub/api/nodes" and "/hub/node/id/".
func (s *hubServer) hubPath(p string) string {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if s.basePath == "" {
		return p
	}
	if p == "/" {
		return s.basePath + "/"
	}
	return s.basePath + p
}

func (s *hubServer) hubNodeMount(id string) string {
	return s.hubPath("/node/" + id)
}

// mountBasePath lets the hub run behind a reverse-proxy sub-path without nginx
// rewriting internal routes. Requests outside the mount are 404; the exact
// mount path redirects to its slash form so browser-relative fetches like
// "api/nodes" resolve under the mount.
func (s *hubServer) mountBasePath(next http.Handler) http.Handler {
	if s.basePath == "" {
		return next
	}
	base := s.basePath
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == base {
			target := base + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			return
		}
		if !strings.HasPrefix(r.URL.Path, base+"/") {
			http.NotFound(w, r)
			return
		}

		stripped := strings.TrimPrefix(r.URL.Path, base)
		if stripped == "" {
			stripped = "/"
		}
		r2 := r.Clone(r.Context())
		u := *r.URL
		u.Path = stripped
		if u.RawPath != "" {
			if raw, ok := stripHubRawPathPrefix(u.RawPath, base); ok {
				u.RawPath = raw
			} else {
				u.RawPath = ""
			}
		}
		r2.URL = &u
		next.ServeHTTP(w, r2)
	})
}

func stripHubRawPathPrefix(rawPath, base string) (string, bool) {
	if rawPath == base {
		return "/", true
	}
	prefix := base + "/"
	if strings.HasPrefix(rawPath, prefix) {
		return strings.TrimPrefix(rawPath, base), true
	}
	return "", false
}
