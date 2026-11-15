package providerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// JSONRequestOptions configures the shared authenticated JSON request path used
// by simple provider transports.
type JSONRequestOptions struct {
	Client           *http.Client
	Method           string
	URL              string
	APIKey           string
	Payload          any
	Provider         string
	ExpectedStatus   int
	WrapRequestError func(error) error
	Headers          http.Header
	Debug            bool
	RequestLabel     string
	ResponseLabel    string
	Debugf           func(string, string, ...any)
	MaxResponseBytes int64
	RedactPayload    bool
	OnRequest        func([]byte)
	OnResponse       func(status int, contentType string, body []byte)
}

// DoJSONRequest sends an authenticated JSON request and returns the response body and content type.
func DoJSONRequest(ctx context.Context, opts JSONRequestOptions) ([]byte, string, error) {
	resp, err := OpenJSONRequest(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := ReadBounded(resp.Body, opts.MaxResponseBytes)
	if err != nil {
		return nil, "", fmt.Errorf("read %s response: %w", opts.Provider, err)
	}
	contentType := resp.Header.Get("Content-Type")
	if opts.Debug && opts.Debugf != nil {
		opts.Debugf(opts.ResponseLabel, "status=%d content-type=%s body_len=%d", resp.StatusCode, contentType, len(body))
	}
	if opts.OnResponse != nil {
		opts.OnResponse(resp.StatusCode, contentType, body)
	}
	expected := opts.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	if resp.StatusCode != expected {
		return nil, "", NewStatusError(opts.Provider, resp, body)
	}
	return body, contentType, nil
}

// OpenJSONRequest sends the shared authenticated request but leaves body streaming
// and status handling to the caller. The caller must close the response body.
func OpenJSONRequest(ctx context.Context, opts JSONRequestOptions) (*http.Response, error) {
	jsonBody, err := json.Marshal(opts.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s request: %w", opts.Provider, err)
	}
	if opts.Debug && opts.Debugf != nil {
		opts.Debugf(opts.RequestLabel, "%s %s\n%s", opts.Method, opts.URL, requestDebugBody(opts, jsonBody))
	}
	if opts.OnRequest != nil {
		opts.OnRequest(jsonBody)
	}
	req, err := http.NewRequestWithContext(ctx, opts.Method, opts.URL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", opts.Provider, err)
	}
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range opts.Headers {
		req.Header.Del(key)
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if opts.WrapRequestError != nil {
			return nil, opts.WrapRequestError(err)
		}
		return nil, err
	}
	return resp, nil
}

func requestDebugBody(opts JSONRequestOptions, body []byte) string {
	if opts.RedactPayload {
		return fmt.Sprintf("[redacted payload: %d bytes]", len(body))
	}
	return string(body)
}

// ReadBounded reads at most limit bytes; zero preserves the legacy unbounded path.
func ReadBounded(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return body, nil
}
