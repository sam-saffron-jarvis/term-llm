package proxyconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type connectFixture struct {
	mu                            sync.Mutex
	root                          string
	action, status                string
	start                         connectStart
	starts, commits, native, acks int
	credential                    string
	loseCommit, loseAck           bool
	result                        *ConnectResult
}

func newConnectFixture(t *testing.T, action string) (*connectFixture, *httptest.Server) {
	t.Helper()
	f := &connectFixture{action: action, status: "pending"}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	f.root = server.URL
	t.Cleanup(server.Close)
	return f, server
}
func (f *connectFixture) response() ConnectRequest {
	out := ConnectRequest{ID: f.start.ID, Code: "11223344", Origin: f.root, Expires: time.Now().Add(5 * time.Minute).Unix(), Status: f.status, Action: f.start.Action, Account: f.start.Account, ClientName: f.start.ClientName, Route: f.start.Account, Prefix: "/" + f.start.Account + "/", Result: f.result}
	if f.status != "pending" {
		out.ResolvedAction = f.action
	}
	return out
}
func (f *connectFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Origin") != f.root || r.Header.Get("Cookie") != "" {
		w.WriteHeader(403)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/_proxy/api/connect/") {
	case "start":
		var in connectStart
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		if f.start.ID != "" && (f.start != in) {
			w.WriteHeader(409)
			return
		}
		f.starts++
		f.start = in
	case "status":
		if r.URL.Query().Get("id") != f.start.ID || r.Header.Get("Authorization") != "Bearer "+f.start.Secret {
			w.WriteHeader(401)
			return
		}
	case "commit":
		var in struct{ ID, Token string }
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID != f.start.ID || r.Header.Get("Authorization") != "Bearer "+f.start.Secret {
			w.WriteHeader(401)
			return
		}
		f.commits++
		f.credential = in.Token
		f.status = "complete"
		f.result = &ConnectResult{GatewayURL: f.root + "/" + f.start.Account + "/", Account: f.start.Account, Route: f.start.Account}
		if f.start.ClientName != "" {
			f.result.Token = "sk-tlp-" + strings.Repeat("a", 64)
			f.result.CallerID = "c_fixture_client"
		}
		if f.loseCommit {
			f.loseCommit = false
			h := w.(http.Hijacker)
			conn, _, _ := h.Hijack()
			conn.Close()
			return
		}
	case "ack":
		f.acks++
		f.status = "acknowledged"
		if f.result != nil {
			f.result.Token = ""
		}
		if f.loseAck {
			f.loseAck = false
			h := w.(http.Hijacker)
			conn, _, _ := h.Hijack()
			conn.Close()
			return
		}
	case "cancel":
		f.status = "canceled"
	default:
		w.WriteHeader(404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(f.response())
}
func fixtureRunOptions(t *testing.T, f *connectFixture, home string, out io.Writer) Options {
	t.Helper()
	return Options{URL: f.root, Account: "claude", ClientName: "Fixture laptop", Home: home, Env: []string{}, In: strings.NewReader(""), Out: out, PollInterval: time.Millisecond, Confirm: func(string) (bool, error) { return true, nil }, OpenBrowser: func(string) error { f.mu.Lock(); defer f.mu.Unlock(); f.status = "approved"; return nil }, AcquireToken: func(context.Context, string, io.Reader) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.native++
		return "sk-ant-oat01-" + strings.Repeat("Q", 64), nil
	}}
}
func TestConnectFirstLoginAndDevicePairUseSameScopedFlow(t *testing.T) {
	for _, action := range []string{"add", "device"} {
		t.Run(action, func(t *testing.T) {
			f, _ := newConnectFixture(t, action)
			home := t.TempDir()
			var out bytes.Buffer
			global := filepath.Join(home, ".config", "term-llm", "config.yaml")
			os.MkdirAll(filepath.Dir(global), 0700)
			original := []byte("providers:\n  unrelated:\n    model: fixture\n")
			os.WriteFile(global, original, 0600)
			opts := fixtureRunOptions(t, f, home, &out)
			if err := Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			wantNative := 0
			if action == "add" {
				wantNative = 1
			}
			if f.native != wantNative || f.commits != 1 {
				t.Fatalf("native=%d commits=%d", f.native, f.commits)
			}
			if action == "device" && f.credential != "" {
				t.Fatal("pair-only uploaded upstream credential")
			}
			raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte("sk-tlp-")) || bytes.Contains(raw, []byte("sk-ant-")) {
				t.Fatal("worker config did not contain only scoped proxy credential")
			}
			if bytes.Contains(out.Bytes(), []byte("sk-tlp-")) || bytes.Contains(out.Bytes(), []byte("sk-ant-")) {
				t.Fatal("credential echoed")
			}
			after, _ := os.ReadFile(global)
			if !bytes.Equal(after, original) {
				t.Fatal("global term-llm config was modified")
			}
			info, _ := os.Stat(filepath.Join(home, ".claude", "settings.json"))
			if info.Mode().Perm() != 0600 {
				t.Fatal("settings not private")
			}
		})
	}
}
func TestConnectUncertainCommitPollsReceiptWithoutMintReplay(t *testing.T) {
	f, _ := newConnectFixture(t, "add")
	f.loseCommit = true
	var out bytes.Buffer
	opts := fixtureRunOptions(t, f, t.TempDir(), &out)
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if f.commits != 1 || f.native != 1 {
		t.Fatalf("replayed commit/native: %+v", f)
	}
}
func TestConnectUncertainAckResumesExistingConfiguredClient(t *testing.T) {
	f, _ := newConnectFixture(t, "add")
	f.loseAck = true
	var out bytes.Buffer
	home := t.TempDir()
	opts := fixtureRunOptions(t, f, home, &out)
	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("lost acknowledgement not reported")
	}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if f.commits != 1 || f.native != 1 || f.starts != 2 {
		t.Fatalf("duplicate mint on resume: commits=%d native=%d starts=%d", f.commits, f.native, f.starts)
	}
}
func TestConnectExistingConfiguredReconnectKeepsWorkerIdentity(t *testing.T) {
	f, _ := newConnectFixture(t, "add")
	var out bytes.Buffer
	home := t.TempDir()
	opts := fixtureRunOptions(t, f, home, &out)
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	f.mu.Lock()
	f.start = connectStart{}
	f.status = "pending"
	f.result = nil
	f.action = "reconnect"
	f.mu.Unlock()
	opts.Reconnect = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !bytes.Equal(before, after) || f.start.ClientName != "" || f.result.Token != "" || f.native != 2 {
		t.Fatal("account-only reconnect changed worker identity")
	}
}
func TestConnectCanceledWaitResumesSavedCapability(t *testing.T) {
	f, _ := newConnectFixture(t, "device")
	var out bytes.Buffer
	opts := fixtureRunOptions(t, f, t.TempDir(), &out)
	approve := opts.OpenBrowser
	opts.OpenBrowser = func(string) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, opts); err == nil {
		t.Fatal("canceled connect succeeded")
	}
	opts.OpenBrowser = approve
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if f.commits != 1 || f.native != 0 {
		t.Fatal("resume minted or signed in twice")
	}
}
func TestConnectLocalDeclineAndAuthConflictDoNotUploadToken(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "decline", true: "conflict"}[conflict], func(t *testing.T) {
			f, _ := newConnectFixture(t, "add")
			var out bytes.Buffer
			opts := fixtureRunOptions(t, f, t.TempDir(), &out)
			if conflict {
				opts.Env = []string{"ANTHROPIC_API_KEY=fixture-secret-value"}
			} else {
				n := 0
				opts.Confirm = func(string) (bool, error) { n++; return n == 1, nil }
			}
			if err := Run(context.Background(), opts); err == nil {
				t.Fatal("unapproved local change succeeded")
			}
			if f.commits != 0 || f.native != 0 {
				t.Fatal("token operation before local approval")
			}
			if strings.Contains(out.String(), "fixture-secret-value") {
				t.Fatal("conflicting credential echoed")
			}
		})
	}
}
func TestConnectDisconnectKeepsLaterUnrelatedSettings(t *testing.T) {
	f, _ := newConnectFixture(t, "device")
	var out bytes.Buffer
	home := t.TempDir()
	opts := fixtureRunOptions(t, f, home, &out)
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	raw, _ := os.ReadFile(path)
	var data map[string]any
	json.Unmarshal(raw, &data)
	data["hooks"] = map[string]any{"fixture": "keep"}
	updated, _ := json.Marshal(data)
	os.WriteFile(path, updated, 0600)
	if err := Disconnect(opts); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Contains(after, []byte("hooks")) || bytes.Contains(after, []byte("ANTHROPIC_AUTH_TOKEN")) {
		t.Fatal("undo damaged unrelated settings or left credential")
	}
}
func TestConnectOriginRedirectAndErrorSafety(t *testing.T) {
	for _, raw := range []string{"https://user:secret@example.com", "http://example.com", "https://example.com/path", "https://example.com/?token=secret", "https://example.com/#secret"} {
		if _, err := normalizedConnectURL(raw); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	var received bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received = true }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer server.Close()
	client := newConnectClient(server.URL, nil)
	_, err := client.start(context.Background(), connectStart{Secret: "private-cap"})
	if err == nil || received || strings.Contains(err.Error(), "private-cap") {
		t.Fatal("redirect followed or credential exposed")
	}
}

func TestConnectDoesNotClaimConfiguredWhenOwnedSettingsWereRemoved(t *testing.T) {
	f, _ := newConnectFixture(t, "device")
	var out bytes.Buffer
	home := t.TempDir()
	opts := fixtureRunOptions(t, f, home, &out)
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	previousStarts := f.starts
	if err := Run(context.Background(), opts); err == nil {
		t.Fatal("stale connector metadata was trusted without settings")
	}
	if f.starts != previousStarts || f.commits != 1 {
		t.Fatal("settings conflict silently enrolled another client")
	}
}
