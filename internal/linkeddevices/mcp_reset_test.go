package linkeddevices

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/mcp"
	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
	"github.com/term-llm/term-llm/internal/pairing"
	"golang.org/x/oauth2"
)

func TestResetRetainsMCPDisposalRecovery(t *testing.T) {
	for _, failure := range []string{"config", "credentials"} {
		for _, mismatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mismatch=%t", failure, mismatch), func(t *testing.T) {
				r, opts, id, accountPath := newResetMCPFixture(t)
				identityPath := filepath.Join(r.dir, "identity.json")
				identity, err := os.ReadFile(identityPath)
				if err != nil {
					t.Fatal(err)
				}
				if mismatch {
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
					opts.StoreInstanceID = "replacement-store"
					r, err = New(opts)
					if err != nil {
						t.Fatal(err)
					}
					if !r.resetRequired || r.grants != nil {
						t.Fatal("fixture did not stop loading at the target mismatch")
					}
				}
				defer func() { _ = r.Close() }()

				var repair func()
				if failure == "config" {
					path, err := mcp.DefaultConfigPath()
					if err != nil {
						t.Fatal(err)
					}
					original, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
					repair = func() {
						if err := os.WriteFile(path, original, 0o600); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					// A non-regular target fails closed even when running as root.
					if err := os.Remove(accountPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(accountPath, 0o700); err != nil {
						t.Fatal(err)
					}
					repair = func() {
						if err := os.Remove(accountPath); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := r.Reset(context.Background()); err == nil {
					t.Fatal("Reset succeeded despite failed MCP disposal")
				}
				grants, err := pairing.OpenGrantStore(filepath.Join(r.dir, "devices.json"))
				if err != nil {
					t.Fatal(err)
				}
				grant, err := grants.Get(id)
				if err != nil || grant.RevokedAt == nil {
					t.Fatalf("reset lost its revoked cleanup record: %+v, %v", grant, err)
				}
				if after, err := os.ReadFile(identityPath); err != nil || string(after) != string(identity) {
					t.Fatalf("failed cleanup replaced the identity: %v", err)
				}
				if r.settings.Enabled {
					t.Fatal("failed Reset left sharing enabled")
				}

				// Recovery must use disk records, not the previous runtime.
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				repair()
				r, err = New(opts)
				if err != nil {
					t.Fatal(err)
				}
				if !mismatch {
					assertResetMCPDisposed(t, accountPath)
				}
				if err := r.Reset(context.Background()); err != nil {
					t.Fatalf("retry Reset: %v", err)
				}
				assertResetMCPDisposed(t, accountPath)
				if devices, err := r.grants.List(); err != nil || len(devices) != 0 {
					t.Fatalf("retry did not finish Reset: %v, %v", devices, err)
				}
				if after, err := os.ReadFile(identityPath); err != nil || string(after) == string(identity) {
					t.Fatalf("successful Reset did not replace identity: %v", err)
				}
			})
		}
	}
}

func TestResetRefusesToForgetUnreadableGrants(t *testing.T) {
	r, _, _, _ := newResetMCPFixture(t)
	defer r.Close()
	path := filepath.Join(r.dir, "devices.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Reset(context.Background()); err == nil {
		t.Fatal("Reset silently forgot unreadable cleanup records")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{" {
		t.Fatalf("Reset removed unreadable grants: %q, %v", data, err)
	}
}

func TestInterruptedResetMCPDisposalRecoversAtStartup(t *testing.T) {
	r, opts, id, accountPath := newResetMCPFixture(t)
	// Model a process stopping after durable revocation but before disposal.
	if err := r.stopLocked(); err != nil {
		t.Fatal(err)
	}
	if err := r.grants.Revoke(id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertResetMCPDisposed(t, accountPath)
}

func newResetMCPFixture(t *testing.T) (*Runtime, Options, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(BrokerURLEnv, "")
	opts := Options{DataDir: t.TempDir(), Target: "reset", StoreInstanceID: "store", Authenticated: true, Handler: http.NotFoundHandler()}
	r, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	id := base64.RawURLEncoding.EncodeToString(make([]byte, pairing.DeviceIDSize))
	if err := r.grants.Add(pairing.Grant{
		DeviceID: id, DeviceKey: make([]byte, ed25519.PublicKeySize), Scope: pairing.ScopeWebV1,
		BasePath: "/", ReconnectCapabilityHash: make([]byte, sha256.Size), PairedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.grants.SetManageMCP(id, true); err != nil {
		t.Fatal(err)
	}
	if err := mcp.UpdateConfig(func(cfg *mcp.Config) error {
		cfg.AddServer("mine", mcp.ServerConfig{URL: "https://remote.example/mcp", LinkedInstaller: id})
		cfg.AddServer("host", mcp.ServerConfig{URL: "https://remote.example/mcp", LinkedDevices: true})
		cfg.AddServer("other", mcp.ServerConfig{URL: "https://remote.example/mcp", LinkedInstaller: "other-device"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	storePath, err := mcpoauth.DefaultStorePath()
	if err != nil {
		t.Fatal(err)
	}
	accountPath := filepath.Join(filepath.Dir(storePath), "mcp-linked", fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
	_, err = mcpoauth.NewFileStore(accountPath).Update("https://remote.example/mcp", func(*mcpoauth.Session) (*mcpoauth.Session, error) {
		return &mcpoauth.Session{Config: mcpoauth.OAuth2Config{ClientID: "client"}, Token: &oauth2.Token{AccessToken: "test"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if mcpoauth.ForInstaller(id).Status("https://remote.example/mcp").State != mcpoauth.AuthSignedIn {
		t.Fatal("fixture did not bind the live installer coordinator")
	}
	return r, opts, id, accountPath
}

func assertResetMCPDisposed(t *testing.T, accountPath string) {
	t.Helper()
	cfg, err := mcp.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Servers["mine"]; ok || len(cfg.Servers) != 2 || cfg.Servers["host"].URL == "" || cfg.Servers["other"].URL == "" {
		t.Fatalf("reset did not preserve only unrelated installations: %v", cfg.ServerNames())
	}
	if _, err := os.Stat(accountPath); !os.IsNotExist(err) {
		t.Fatalf("device credentials survived recovery: %v", err)
	}
}
