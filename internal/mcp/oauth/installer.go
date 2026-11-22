package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrInstallerRetired reports that a linked device's MCP accounts were disposed.
var ErrInstallerRetired = errors.New("linked-device MCP accounts were removed")

func installerStorePath(id string) (string, error) {
	path, err := DefaultStorePath()
	return filepath.Join(filepath.Dir(path), "mcp-linked", fmt.Sprintf("%x.json", sha256.Sum256([]byte(id)))), err
}

// Retired reports whether this coordinator's linked-device store was disposed.
func (c *Coordinator) Retired() bool {
	store, ok := c.store.(*FileStore)
	return ok && store.retired.Load()
}

// Retire deletes the store under its locks. Operations already holding them
// finish first; every later operation on s fails with ErrInstallerRetired.
func (s *FileStore) Retire() error {
	err := s.withLock(func() error {
		s.retired.Store(true)
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove MCP OAuth store: %w", err)
		}
		return nil
	})
	s.retired.Store(true)
	if errors.Is(err, ErrInstallerRetired) {
		return nil
	}
	return err
}

// RetireInstaller disposes one linked device's MCP accounts: the store is
// sealed and deleted, then pending sign-ins are cancelled. In-process users of
// the old coordinator fail closed; a later ForInstaller(id) starts empty.
func RetireInstaller(id string) error {
	if id == "" {
		return errors.New("installer is required")
	}
	path, err := installerStorePath(id)
	if err != nil {
		return err
	}
	value, live := linkedCoordinators.LoadAndDelete(path)
	if !live {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			return nil // Never signed in during this process or since disposal.
		}
		return NewFileStore(path).Retire()
	}
	c := value.(*Coordinator)
	store, ok := c.store.(*FileStore)
	if !ok {
		return fmt.Errorf("linked MCP account store is not file-backed")
	}
	err = store.Retire()
	c.cancelFlows()
	return err
}

func (c *Coordinator) cancelFlows() {
	c.mu.Lock()
	pending := make(map[string]string, len(c.flows))
	for id, record := range c.flows {
		pending[id] = record.flow.Endpoint
	}
	c.mu.Unlock()
	for id, endpoint := range pending {
		c.cancelFlow(endpoint, id, true)
	}
}

// ForgetInstallation fences sign-in writes, including after callback delivery,
// and deletes the installer's canonical endpoint account without contacting the
// provider. Installations at that same resource share the account and flow.
func ForgetInstallation(ctx context.Context, id, endpoint string) error {
	if id == "" {
		return nil
	}
	c := ForInstaller(id)
	if canonical, err := CanonicalEndpoint(endpoint); err == nil {
		c.mu.Lock()
		flowID := c.byEndpoint[canonical]
		c.mu.Unlock()
		if flowID != "" {
			c.cancelFlow(canonical, flowID, true)
		}
	}
	return c.Logout(ctx, endpoint, true)
}
