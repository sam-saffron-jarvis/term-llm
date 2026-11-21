package mcp

import (
	"errors"
	"fmt"

	mcpoauth "github.com/term-llm/term-llm/internal/mcp/oauth"
)

// DisposeInstallers removes every connection the given linked devices
// installed, then their MCP accounts. Host-owned connections are untouched.
// Config goes first so linkedInstallCurrent rejects any later start.
func DisposeInstallers(installers ...string) error {
	owned := make(map[string]bool, len(installers))
	for _, id := range installers {
		if id != "" {
			owned[id] = true
		}
	}
	if len(owned) == 0 {
		return nil
	}
	unchanged := errors.New("no linked installations")
	err := UpdateConfig(func(cfg *Config) error {
		removed := false
		for name, server := range cfg.Servers {
			if owned[server.LinkedInstaller] {
				delete(cfg.Servers, name)
				removed = true
			}
		}
		if !removed {
			return unchanged
		}
		return nil
	})
	if errors.Is(err, unchanged) {
		err = nil
	}
	for id := range owned {
		err = errors.Join(err, mcpoauth.RetireInstaller(id))
	}
	return err
}

// linkedInstallCurrent rechecks a linked installation on disk after its
// coordinator is bound, so a stale in-memory config cannot revive it.
func linkedInstallCurrent(name string, server ServerConfig, coordinator *mcpoauth.Coordinator) error {
	if server.LinkedInstaller == "" {
		return nil
	}
	removed := fmt.Errorf("MCP server %s was removed with its linked device", name)
	if coordinator == nil || coordinator.Retired() {
		return removed
	}
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("verify MCP server %s: %w", name, err)
	}
	if current, ok := cfg.Servers[name]; !ok || current.LinkedInstaller != server.LinkedInstaller || current.URL != server.URL {
		return removed
	}
	return nil
}
