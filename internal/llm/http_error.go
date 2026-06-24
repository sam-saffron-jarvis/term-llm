package llm

import (
	"fmt"
	"net/http"

	"github.com/samsaffron/term-llm/internal/providerhttp"
)

// HTTPStatusError is the shared typed HTTP status error used by provider
// implementations. It preserves response headers so Retry-After can be handled
// centrally by the retry wrapper.
type HTTPStatusError = providerhttp.StatusError

// NewHTTPStatusError returns a typed provider HTTP status error.
func NewHTTPStatusError(provider string, resp *http.Response, body []byte) *HTTPStatusError {
	return providerhttp.NewStatusError(provider, resp, body)
}

// NewHTTPStatusErrorString returns a typed provider HTTP status error using
// already-normalized response metadata/body text.
func NewHTTPStatusErrorString(provider string, statusCode int, status string, header http.Header, body string) *HTTPStatusError {
	return providerhttp.NewStatusErrorString(provider, statusCode, status, header, body)
}

func newHTTPStatusError(provider string, resp *http.Response, body []byte) *HTTPStatusError {
	return NewHTTPStatusError(provider, resp, body)
}

func newHTTPStatusErrorMessageFromResponse(resp *http.Response, buildMessage func(body []byte) string) *HTTPStatusError {
	return providerhttp.NewStatusErrorMessageFromResponse(resp, buildMessage)
}

func newHTTPStatusErrorMessageFromResponsef(resp *http.Response, format string) *HTTPStatusError {
	return newHTTPStatusErrorMessageFromResponse(resp, func(body []byte) string {
		return fmt.Sprintf(format, resp.StatusCode, string(body))
	})
}

func newHTTPStatusErrorString(provider string, statusCode int, status string, header http.Header, body string) *HTTPStatusError {
	return NewHTTPStatusErrorString(provider, statusCode, status, header, body)
}

func newHTTPStatusErrorMessage(message string, resp *http.Response, body []byte) *HTTPStatusError {
	return providerhttp.NewStatusErrorMessage(message, resp, body)
}

func newHTTPStatusErrorMessageString(message string, statusCode int, status string, header http.Header, body string) *HTTPStatusError {
	return providerhttp.NewStatusErrorMessageString(message, statusCode, status, header, body)
}

func newHTTPStatusErrorMessagef(resp *http.Response, body []byte, format string, args ...any) *HTTPStatusError {
	return newHTTPStatusErrorMessage(fmt.Sprintf(format, args...), resp, body)
}

func newHTTPStatusErrorWithDisplayBody(provider string, resp *http.Response, body []byte, displayBody string) *HTTPStatusError {
	display := providerhttp.NewStatusErrorString(provider, resp.StatusCode, resp.Status, resp.Header, displayBody)
	return newHTTPStatusErrorMessage(display.Error(), resp, body)
}
