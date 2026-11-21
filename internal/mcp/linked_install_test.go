package mcp

import (
	"os"
	"testing"
)

func TestDisposeInstallersRemovesOnlyDeviceOwnedConnections(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := DisposeInstallers("nobody"); err != nil {
		t.Fatal(err)
	}
	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no-op disposal wrote config: %v", err)
	}
	if err := UpdateConfig(func(cfg *Config) error {
		cfg.AddServer("host", ServerConfig{Type: "http", URL: "https://host.example/mcp", LinkedDevices: true})
		cfg.AddServer("mine", ServerConfig{Type: "http", URL: "https://remote.example/mcp", LinkedInstaller: "device-a"})
		cfg.AddServer("theirs", ServerConfig{Type: "http", URL: "https://remote.example/mcp", LinkedInstaller: "device-b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	bound := NewClient("mine", cfg.Servers["mine"]) // e.g. a session with stale config
	if err := linkedInstallCurrent("mine", cfg.Servers["mine"], bound.oauthCoordinator); err != nil {
		t.Fatalf("current installation rejected: %v", err)
	}

	if err := DisposeInstallers("device-a"); err != nil {
		t.Fatal(err)
	}
	after, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Servers["mine"]; ok || len(after.Servers) != 2 {
		t.Fatalf("servers after disposal: %v", after.ServerNames())
	}
	if _, err := bound.createHTTPTransport(); err == nil {
		t.Fatal("client bound before disposal could still connect")
	}
	if _, err := NewClient("mine", cfg.Servers["mine"]).createHTTPTransport(); err == nil {
		t.Fatal("stale config revived a disposed installation")
	}
	if _, err := NewClient("theirs", after.Servers["theirs"]).createHTTPTransport(); err != nil {
		t.Fatalf("another device's installation: %v", err)
	}
}
