package pairing

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"
)

func TestGrantCacheSeesEveryOnDiskChange(t *testing.T) {
	_, device, clock := newTestHost(t)
	dir := t.TempDir() + "/private"
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := dir + "/grants.json"
	store, err := OpenGrantStore(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := func(n byte) Grant {
		return Grant{DeviceID: B64(bytes.Repeat([]byte{n}, DeviceIDSize)).String(), Scope: ScopeWebV1, BasePath: "/", DeviceName: "phone-" + string('a'+n), DeviceKey: B64(device.PublicKey()), ReconnectCapabilityHash: make(B64, sha256.Size), PairedAt: clock.nowTime}
	}
	a, b := grant(1), grant(2)
	for _, g := range []Grant{a, b} {
		if err := store.Add(g); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	age := func() {
		t.Helper()
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	loads := 0
	testHookAfterLstat = func(string) { loads++ }
	t.Cleanup(func() { testHookAfterLstat = nil })
	authorized := func(id string) bool {
		_, err := store.Authorize(id, device.PublicKey())
		return err == nil
	}

	age()
	for range 5 {
		if !authorized(a.DeviceID) {
			t.Fatal("active grant denied")
		}
	}
	if loads != 1 {
		t.Fatalf("unchanged grants file read %d times, want 1", loads)
	}

	// Another process revokes; even a restored old mtime cannot hide it.
	other, err := OpenGrantStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Revoke(a.DeviceID, clock.nowTime); err != nil {
		t.Fatal(err)
	}
	age()
	if authorized(a.DeviceID) {
		t.Fatal("external revocation was served from cache")
	}

	// Same size and mtime, new file: the swap is still detected.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	swapped := bytes.Replace(data, []byte(b.DeviceID), []byte(grant(3).DeviceID), 1)
	if len(swapped) != len(data) || bytes.Equal(swapped, data) {
		t.Fatal("test fixture did not swap one same-length device")
	}
	if err := writePrivateFile(path, swapped); err != nil {
		t.Fatal(err)
	}
	age()
	if authorized(b.DeviceID) || !authorized(grant(3).DeviceID) {
		t.Fatal("replaced grants file was served from cache")
	}

	// A file modified just before it was read is never trusted from cache.
	if err := other.SetManageMCP(grant(3).DeviceID, true); err != nil {
		t.Fatal(err)
	}
	loads = 0
	for range 3 {
		authorized(grant(3).DeviceID)
	}
	if loads != 3 {
		t.Fatalf("racily fresh grants file read %d times, want 3", loads)
	}
	if err := other.Remove(grant(3).DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(grant(3).DeviceID, device.PublicKey()); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("removed grant: %v", err)
	}
}
