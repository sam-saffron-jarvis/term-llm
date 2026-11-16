package proxyclient

import "net/http"

// Annotate attaches explicit application attribution for this client. The proxy
// strips its private namespace before forwarding; direct vendor traffic is not
// changed. Other applications may supply their own bounded label.
func Annotate(endpoint string, header http.Header) http.Header {
	copied := header.Clone()
	if copied == nil {
		copied = make(http.Header)
	}
	if _, bound := Lookup(endpoint); bound && copied.Get("X-Term-LLM-Proxy-App") == "" {
		copied.Set("X-Term-LLM-Proxy-App", "term-llm")
	}
	return copied
}
