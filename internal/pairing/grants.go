package pairing

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// ScopeWebV1 authorizes the versioned, base-path-confined web bridge.
const ScopeWebV1 = "web-v1"

// Grant is durable until explicitly revoked or its state is reset. The raw
// reconnect capability is never persisted, only its SHA-256 hash.
type Grant struct {
	ManageMCP               bool       `json:"manage_mcp,omitempty"`
	ApprovedBy              *Person    `json:"approved_by,omitempty"`
	DeviceID                string     `json:"device_id"`
	DeviceKey               B64        `json:"device_key"`
	DeviceName              string     `json:"device_name"`
	Scope                   string     `json:"scope"`
	BasePath                string     `json:"base_path"`
	ReconnectCapabilityHash B64        `json:"reconnect_capability_hash"`
	PairedAt                time.Time  `json:"paired_at"`
	LastSeenAt              *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt               *time.Time `json:"revoked_at,omitempty"`
}

func (g Grant) DeviceFingerprint() string { return KeyFingerprint(g.DeviceKey) }
func (g Grant) Check() error {
	if g.Scope != ScopeWebV1 {
		return fmt.Errorf("%w: grant scope %q", ErrInvalid, g.Scope)
	}
	if g.RevokedAt != nil {
		return ErrRevoked
	}
	return nil
}

type grantFile struct {
	V      int     `json:"v"`
	Grants []Grant `json:"grants"`
}

type GrantStore struct {
	path string
	mu   sync.Mutex
	// Last locked read and the file it came from. Writers replace the file
	// atomically under the file lock, so any write changes this signature.
	cached   grantFile
	cachedAs fs.FileInfo
	cachedAt time.Time
}

// Like Git's racily-clean rule: a file modified within this window of being
// read may change again without a visible mtime change, so it is not trusted.
const grantCacheRacyWindow = 2 * time.Second

func sameGrantFile(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func OpenGrantStore(path string) (*GrantStore, error) {
	s := &GrantStore{path: path}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if _, _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *GrantStore) Path() string { return s.path }
func (s *GrantStore) load() (grantFile, fs.FileInfo, error) {
	data, info, err := readPrivateFileInfo(s.path)
	if isNotExist(err) {
		return grantFile{V: StateVersion}, nil, nil
	}
	if err != nil {
		return grantFile{}, nil, fmt.Errorf("load grants: %w", err)
	}
	var f grantFile
	if err := json.Unmarshal(data, &f); err != nil {
		return grantFile{}, nil, fmt.Errorf("decode grants %s: %w", s.path, err)
	}
	if f.V != StateVersion {
		return grantFile{}, nil, fmt.Errorf("grants %s: unsupported state version %d", s.path, f.V)
	}
	return f, info, nil
}
func (s *GrantStore) update(fn func(*grantFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachedAs = nil
	return withFileLock(s.path, func() error {
		f, _, err := s.load()
		if err != nil {
			return err
		}
		if err := fn(&f); err != nil {
			return err
		}
		data, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			return err
		}
		return writePrivateFile(s.path, data)
	})
}

// read serves an unchanged file from memory. Any change to the file by any
// process, including revocation, is seen by the next read.
func (s *GrantStore) read() (grantFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, err := os.Lstat(s.path); err == nil && s.cachedAs != nil && sameGrantFile(s.cachedAs, info) && checkPrivateFile(s.path, info) == nil && s.cachedAt.Sub(info.ModTime()) > grantCacheRacyWindow {
		return grantFile{V: s.cached.V, Grants: slices.Clone(s.cached.Grants)}, nil
	}
	s.cachedAs = nil
	var f grantFile
	err := withFileLock(s.path, func() error {
		var err error
		var info fs.FileInfo
		f, info, err = s.load()
		if err == nil && info != nil {
			s.cached, s.cachedAs, s.cachedAt = grantFile{V: f.V, Grants: slices.Clone(f.Grants)}, info, time.Now()
		}
		return err
	})
	return f, err
}
func (s *GrantStore) List() ([]Grant, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(f.Grants, func(i, j int) bool { return f.Grants[i].PairedAt.Before(f.Grants[j].PairedAt) })
	return f.Grants, nil
}
func (s *GrantStore) Get(deviceID string) (Grant, error) {
	f, err := s.read()
	if err != nil {
		return Grant{}, err
	}
	for _, g := range f.Grants {
		if subtle.ConstantTimeCompare([]byte(g.DeviceID), []byte(deviceID)) == 1 {
			return g, nil
		}
	}
	return Grant{}, ErrGrantNotFound
}
func (s *GrantStore) Add(g Grant) error {
	id, err := DecodeB64(g.DeviceID)
	if err != nil || len(id) != DeviceIDSize || g.Scope != ScopeWebV1 || g.BasePath == "" || len(g.DeviceKey) != ed25519.PublicKeySize || len(g.ReconnectCapabilityHash) != sha256.Size || g.PairedAt.IsZero() {
		return fmt.Errorf("%w: incomplete grant", ErrInvalid)
	}
	return s.update(func(f *grantFile) error {
		for _, existing := range f.Grants {
			if existing.DeviceID == g.DeviceID {
				return fmt.Errorf("%w: duplicate device id", ErrInvalid)
			}
		}
		// Pairing never confers management rights. Only a later owner action can.
		g.ManageMCP = false
		f.Grants = append(f.Grants, g)
		return nil
	})
}

// SetManageMCP changes one active grant without replacing its device identity.
func (s *GrantStore) SetManageMCP(id string, enabled bool) error {
	return s.update(func(f *grantFile) error {
		for i := range f.Grants {
			g := &f.Grants[i]
			if g.DeviceID == id && g.RevokedAt == nil {
				g.ManageMCP = enabled
				return nil
			}
		}
		return ErrGrantNotFound
	})
}
func (s *GrantStore) Revoke(deviceID string, now time.Time) error {
	return s.update(func(f *grantFile) error {
		for i := range f.Grants {
			if f.Grants[i].DeviceID == deviceID {
				if f.Grants[i].RevokedAt == nil {
					t := now.UTC()
					f.Grants[i].RevokedAt = &t
				}
				return nil
			}
		}
		return ErrGrantNotFound
	})
}
func (s *GrantStore) Remove(deviceID string) error {
	return s.update(func(f *grantFile) error {
		for i := range f.Grants {
			if f.Grants[i].DeviceID == deviceID {
				f.Grants = append(f.Grants[:i], f.Grants[i+1:]...)
				return nil
			}
		}
		return ErrGrantNotFound
	})
}

// Authorize performs a live, revocation-aware device identity check.
func (s *GrantStore) Authorize(deviceID string, deviceKey ed25519.PublicKey) (Grant, error) {
	return s.authorize(deviceID, deviceKey, nil, time.Time{}, false)
}

func (s *GrantStore) authorize(deviceID string, deviceKey, reconnectCapability []byte, now time.Time, touch bool) (Grant, error) {
	var out Grant
	check := func(f *grantFile) error {
		for i := range f.Grants {
			g := &f.Grants[i]
			if subtle.ConstantTimeCompare([]byte(g.DeviceID), []byte(deviceID)) != 1 {
				continue
			}
			if !bytes.Equal(g.DeviceKey, deviceKey) {
				return ErrGrantNotFound
			}
			if err := g.Check(); err != nil {
				return err
			}
			if reconnectCapability != nil {
				hash := sha256.Sum256(reconnectCapability)
				if subtle.ConstantTimeCompare(g.ReconnectCapabilityHash, hash[:]) != 1 {
					return ErrGrantNotFound
				}
			}
			if touch {
				t := now.UTC()
				g.LastSeenAt = &t
			}
			out = *g
			return nil
		}
		return ErrGrantNotFound
	}
	if touch {
		if err := s.update(check); err != nil {
			return Grant{}, err
		}
		return out, nil
	}
	f, err := s.read()
	if err != nil {
		return Grant{}, err
	}
	if err := check(&f); err != nil {
		return Grant{}, err
	}
	return out, nil
}
