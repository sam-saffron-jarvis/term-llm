package pairing

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestManageMCPLegacyAndNewGrants(t *testing.T) {
	host, device, clock := newTestHost(t)
	store := host.Grants()
	legacy := Grant{DeviceID: B64(bytes.Repeat([]byte{1}, DeviceIDSize)).String(), Scope: ScopeWebV1, BasePath: "/", DeviceKey: B64(device.PublicKey()), ReconnectCapabilityHash: make(B64, sha256.Size), PairedAt: clock.nowTime}
	// A version-1 grants file written before manage_mcp existed.
	if err := store.update(func(f *grantFile) error { f.Grants = []Grant{legacy}; return nil }); err != nil {
		t.Fatal(err)
	}
	other := legacy
	other.DeviceID = B64(bytes.Repeat([]byte{2}, DeviceIDSize)).String()
	other.ManageMCP = true
	if err := store.Add(other); err != nil {
		t.Fatal(err)
	}
	check := func(id string, want bool) {
		t.Helper()
		reopened, err := OpenGrantStore(store.Path())
		if err != nil {
			t.Fatal(err)
		}
		g, err := reopened.Authorize(id, device.PublicKey())
		if err != nil || g.ManageMCP != want {
			t.Fatalf("grant %s: permission %v, err %v; want %v", id, g.ManageMCP, err, want)
		}
	}
	check(legacy.DeviceID, false)
	check(other.DeviceID, false)
	for _, enabled := range []bool{true, false} {
		if err := store.SetManageMCP(legacy.DeviceID, enabled); err != nil {
			t.Fatal(err)
		}
		check(legacy.DeviceID, enabled)
		check(other.DeviceID, false)
	}
	if err := store.Revoke(legacy.DeviceID, clock.nowTime); err != nil {
		t.Fatal(err)
	}
	if err := store.SetManageMCP(legacy.DeviceID, true); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("changed revoked grant: %v", err)
	}
}
