package proxyconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ConnectRequest is public approval metadata, never a credential container.
type ConnectRequest struct {
	ID             string         `json:"id"`
	Code           string         `json:"code"`
	Origin         string         `json:"origin"`
	Expires        int64          `json:"expires"`
	Status         string         `json:"status"`
	Action         string         `json:"action"`
	ResolvedAction string         `json:"resolved_action,omitempty"`
	Account        string         `json:"account"`
	ClientName     string         `json:"client_name"`
	Route          string         `json:"route"`
	Prefix         string         `json:"prefix"`
	Result         *ConnectResult `json:"result,omitempty"`
}

// ConnectResult contains only a scoped proxy credential, never the subscription token.
type ConnectResult struct {
	Token      string `json:"token,omitempty"`
	CallerID   string `json:"caller_id,omitempty"`
	GatewayURL string `json:"gateway_url"`
	Account    string `json:"account,omitempty"`
	Route      string `json:"route,omitempty"`
}

type connectStart struct {
	ID         string `json:"id"`
	Secret     string `json:"secret"`
	Account    string `json:"account"`
	ClientName string `json:"client_name"`
	Action     string `json:"action"`
}

type connectHTTPError struct{ Status int }

func (e *connectHTTPError) Error() string {
	switch e.Status {
	case 401, 403:
		return "Connection is not approved, or the private pairing capability is invalid"
	case 404:
		return "No matching connection receipt; the existing request has not been replaced"
	case 409:
		return "Connection target changed or collides with existing configuration; inspect it in the browser"
	case 410:
		return "Connection approval expired or was consumed; inspect the receipt before starting again"
	case 429:
		return "Too many pending connections; wait before trying the same request"
	default:
		return "Connection service unavailable; resume this request to inspect its durable receipt"
	}
}

type connectClient struct {
	root string
	http *http.Client
}

func normalizedConnectURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.ForceQuery {
		return "", errors.New("Use the exact proxy HTTPS origin, without credentials, path, query or fragment")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !(strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
			return "", errors.New("Remote pairing requires HTTPS; HTTP is allowed only on loopback")
		}
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", errors.New("Invalid proxy port")
		}
	}
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	u.Path = ""
	u.RawPath = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}
func newConnectClient(root string, client *http.Client) *connectClient {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Transport: transport}
	}
	copy := *client
	copy.Jar = nil
	copy.Timeout = 20 * time.Second
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &connectClient{root: root, http: &copy}
}
func (c *connectClient) request(ctx context.Context, method, path, secret string, body any) (ConnectRequest, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return ConnectRequest{}, errors.New("Could not encode connection request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.root+"/_proxy/api/connect/"+path, bytes.NewReader(data))
	if err != nil {
		return ConnectRequest{}, errors.New("Invalid connection request")
	}
	req.GetBody = nil
	req.Header.Set("Origin", c.root)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ConnectRequest{}, ctx.Err()
		}
		return ConnectRequest{}, errors.New("Connection request outcome unknown; resume the existing request, not a new enrollment")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConnectRequest{}, &connectHTTPError{resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return ConnectRequest{}, errors.New("Connection response unavailable; inspect the existing receipt")
	}
	var out ConnectRequest
	if json.Unmarshal(raw, &out) != nil {
		return out, errors.New("Invalid connection response; inspect the existing receipt")
	}
	return out, nil
}
func (c *connectClient) start(ctx context.Context, in connectStart) (ConnectRequest, error) {
	return c.request(ctx, http.MethodPost, "start", "", in)
}
func (c *connectClient) status(ctx context.Context, id, secret string) (ConnectRequest, error) {
	return c.request(ctx, http.MethodGet, "status?id="+url.QueryEscape(id), secret, nil)
}
func (c *connectClient) commit(ctx context.Context, id, secret, token string) (ConnectRequest, error) {
	return c.request(ctx, http.MethodPost, "commit", secret, struct {
		ID    string `json:"id"`
		Token string `json:"token,omitempty"`
	}{id, token})
}
func (c *connectClient) finish(ctx context.Context, action, id, secret string) error {
	_, err := c.request(ctx, http.MethodPost, action, secret, struct {
		ID string `json:"id"`
	}{id})
	return err
}
