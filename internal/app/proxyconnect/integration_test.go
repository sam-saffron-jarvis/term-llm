package proxyconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	appproxy "github.com/term-llm/term-llm/internal/app/proxy"
)

// This exercises the actual shared API from the actual CLI implementation.
// Default-passkey consent is tested in the owning server and real browser fixture;
// this loopback fixture deliberately uses explicit legacy token admin mode.
func TestConnectCLIUsesRealAtomicAPIForLoginOnceThenPair(t *testing.T) {
	state := t.TempDir()
	adminPath := filepath.Join(state, "admin-token")
	adminToken := strings.Repeat("a", 64)
	if err := os.WriteFile(adminPath, []byte(adminToken), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := appproxy.Open(appproxy.Options{Auth: "token", StateDir: filepath.Join(state, "db"), AdminTokenFile: adminPath})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server := httptest.NewServer(service)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	admin := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	var csrf string
	adminRequest := func(method, path string, body any, target any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+"/_proxy/api/"+path, bytes.NewReader(data))
		req.Header.Set("Origin", server.URL)
		req.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, e := admin.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("admin %s status=%d: %s", path, resp.StatusCode, raw)
		}
		if target != nil {
			if e = json.NewDecoder(resp.Body).Decode(target); e != nil {
				t.Fatal(e)
			}
		}
	}
	var login struct {
		CSRF string `json:"csrf"`
	}
	adminRequest("POST", "login", map[string]string{"token": adminToken}, &login)
	csrf = login.CSRF
	nativeCalls := 0
	var output bytes.Buffer
	run := func(name, action string, home string) {
		t.Helper()
		opts := Options{URL: server.URL, Account: "claude", ClientName: name, Home: home, Env: []string{}, In: strings.NewReader(""), Out: &output, Reconnect: action == "reconnect", PollInterval: time.Millisecond, Confirm: func(string) (bool, error) { return true, nil }, AcquireToken: func(context.Context, string, io.Reader) (string, error) {
			nativeCalls++
			return "sk-ant-oat01-" + strings.Repeat("T", 64), nil
		}}
		opts.OpenBrowser = func(link string) error {
			u, e := url.Parse(link)
			if e != nil {
				t.Fatal(e)
			}
			id := u.Query().Get("connect")
			if u.Query().Has("code") {
				t.Fatal("approval code was put in link")
			}
			matches := regexp.MustCompile(`Type code ([a-f0-9]{8}) from YOUR terminal`).FindAllStringSubmatch(output.String(), -1)
			if len(matches) == 0 {
				t.Fatal("terminal code missing")
			}
			code := matches[len(matches)-1][1]
			var preview ConnectRequest
			adminRequest("GET", "connect/request?id="+id, nil, &preview)
			if name == "second" && preview.ResolvedAction != "device" {
				t.Fatal("working account did not resolve device-only", preview.ResolvedAction)
			}
			var approved ConnectRequest
			adminRequest("POST", "connect/approve", map[string]string{"id": id, "code": code}, &approved)
			return nil
		}
		if e := Run(context.Background(), opts); e != nil {
			t.Fatal(e)
		}
	}
	firstHome, secondHome := t.TempDir(), t.TempDir()
	run("first", "auto", firstHome)
	run("second", "auto", secondHome)
	if nativeCalls != 1 {
		t.Fatalf("second computer ran native login: %d calls", nativeCalls)
	}
	var snapshot struct {
		Accounts []struct {
			AuthMode string `json:"auth_mode"`
			Expires  int64  `json:"expires_at"`
		}
		Routes []struct {
			Name    string `json:"name"`
			Billing string `json:"billing"`
			Usage   string `json:"usage"`
		}
		Callers []struct {
			Name   string   `json:"name"`
			Grants []string `json:"grants"`
		}
	}
	adminRequest("GET", "state", nil, &snapshot)
	if len(snapshot.Accounts) != 1 || snapshot.Accounts[0].AuthMode != "token_only" || snapshot.Accounts[0].Expires != 0 || len(snapshot.Routes) != 1 || snapshot.Routes[0].Billing != "plan" || snapshot.Routes[0].Usage != "auto" || len(snapshot.Callers) != 2 {
		t.Fatal("shared atomic API did not set exact account/route/devices", fmt.Sprintf("accounts=%d routes=%d clients=%d", len(snapshot.Accounts), len(snapshot.Routes), len(snapshot.Callers)))
	}
	for _, caller := range snapshot.Callers {
		if len(caller.Grants) != 1 || caller.Grants[0] != "claude" {
			t.Fatal("grant widened", caller.Grants)
		}
	}
	if strings.Contains(output.String(), "sk-ant-") || strings.Contains(output.String(), "sk-tlp-") || strings.Contains(output.String(), adminToken) {
		t.Fatal("CLI leaked a credential")
	}
	raw, _ := os.ReadFile(filepath.Join(secondHome, ".claude", "settings.json"))
	if bytes.Contains(raw, []byte("sk-ant-")) {
		t.Fatal("upstream token reached second computer")
	}
}
