package server

import (
	"net/http"

	"github.com/term-llm/term-llm/internal/app/serve/wire"
	"github.com/term-llm/term-llm/internal/app/uploads"
	"github.com/term-llm/term-llm/internal/linkeddevices"
)

// LinkedUploadHandler accepts chunks exclusively from authenticated peer connections.
func LinkedUploadHandler(assemblies *uploads.LinkedUploads, writer *uploads.ReferenceHandler, hubURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := linkeddevices.DeviceFromRequest(r)
		if !ok || p.Lifetime == nil || p.Lifetime.Done() == nil || p.Lifetime.Err() != nil {
			wire.WriteOpenAIError(w, http.StatusForbidden, "forbidden", "a connected linked device is required")
			return
		}
		assemblies.HandleChunk(w, r, p.DeviceID, p.Lifetime, writer, hubURL)
	}
}
