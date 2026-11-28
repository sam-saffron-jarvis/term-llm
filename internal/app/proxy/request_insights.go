package proxy

import (
	"io"
	"net/http"

	"github.com/term-llm/term-llm/internal/app/proxy/requestmeta"
)

// Request observation is independent of response evidence and custody dispatch.
// Only the dispatch owner publishes the frozen snapshot, never a Read callback.
func observeRequestBody(body io.ReadCloser, r *http.Request, ar activeRoute, path string) *requestmeta.Observer {
	if ar.Usage != "auto" {
		return nil
	}
	return requestmeta.ForRequest(body, r, ar.target, path, ar.Profile, ar.Upstream)
}
