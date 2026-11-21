package linkeddevices_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/linkeddevices"
	"github.com/term-llm/term-llm/internal/linkeddevices/testdevice"
	"github.com/term-llm/term-llm/internal/mcp"
	"github.com/term-llm/term-llm/internal/peerbroker"
)

func TestDeviceMCPStateIsDisposedWithItsAuthority(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	b, err := peerbroker.NewHandler(peerbroker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	broker := httptest.NewServer(b)
	defer broker.Close()
	started, revoked, wrote := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	opts := linkeddevices.Options{DataDir: t.TempDir(), Target: "dispose", StoreInstanceID: "store", Authenticated: true, BrokerURL: broker.URL, BasePath: "/mount", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := linkeddevices.DeviceFromRequest(r)
		if r.URL.Path == "/v1/mcp/servers" {
			// An install admitted before revocation that writes afterwards. With
			// the drain, Revoke cannot return first; the timeout only matters
			// without it, so it cannot cause a false failure.
			started <- struct{}{}
			<-p.Lifetime.Done()
			select {
			case <-revoked:
			case <-time.After(250 * time.Millisecond):
			}
			_ = mcp.UpdateConfig(func(cfg *mcp.Config) error {
				cfg.AddServer("late", mcp.ServerConfig{Type: "http", URL: "https://late.example/mcp", LinkedInstaller: p.DeviceID})
				return nil
			})
			close(wrote)
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	runtime, err := linkeddevices.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sas := make(chan string, 1)
	device, err := testdevice.New(testdevice.Config{StatePath: t.TempDir() + "/device.json", OnSAS: func(code string) { sas <- code }})
	if err != nil {
		t.Fatal(err)
	}
	pairRuntimeDevice(t, ctx, runtime, device, sas)
	id := runtime.Status().Devices[0].ID
	setManage := func(rt *linkeddevices.Runtime, enabled bool) {
		t.Helper()
		rr := httptest.NewRecorder()
		rt.Handler().ServeHTTP(rr, httptest.NewRequest("POST", "/v1/linked-devices/devices/"+id+"/mcp-management", strings.NewReader(fmt.Sprintf(`{"enabled":%t}`, enabled))))
		if rr.Code != http.StatusNoContent {
			t.Fatalf("set management %t: %d", enabled, rr.Code)
		}
	}
	accounts := filepath.Join(config, "term-llm", "mcp-linked", fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
	seed := func() {
		t.Helper()
		if err := mcp.UpdateConfig(func(cfg *mcp.Config) error {
			cfg.AddServer("host", mcp.ServerConfig{Type: "http", URL: "https://host.example/mcp"})
			cfg.AddServer("other-device", mcp.ServerConfig{Type: "http", URL: "https://remote.example/mcp", LinkedInstaller: "other"})
			cfg.AddServer("mine", mcp.ServerConfig{Type: "http", URL: "https://remote.example/mcp", LinkedInstaller: id})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(accounts), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(accounts, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(want string) {
		t.Helper()
		cfg, err := mcp.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(cfg.ServerNames(), ","); got != want {
			t.Fatalf("servers = %s, want %s", got, want)
		}
		if _, err := os.Stat(accounts); strings.Contains(want, "mine") != (err == nil) {
			t.Fatalf("device account store presence wrong for %s: %v", want, err)
		}
	}

	setManage(runtime, true)
	seed()
	session, err := device.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	if session, err = device.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	expect("host,mine,other-device") // Reconnects keep the device's installs.

	setManage(runtime, false)
	expect("host,other-device")

	setManage(runtime, true)
	seed()
	installed := make(chan struct{})
	go func() {
		defer close(installed)
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://peer/mount/v1/mcp/servers", nil)
		if res, err := session.HTTPClient().Do(req); err == nil {
			res.Body.Close()
		}
	}()
	<-started
	if err := runtime.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	close(revoked)
	<-installed
	<-wrote
	expect("host,other-device") // Includes the in-flight install's late write.
	session.Close()

	// A disposal interrupted before restart is completed by the next start.
	seed()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := linkeddevices.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	expect("host,other-device")
}
