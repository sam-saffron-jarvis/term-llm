package passkeyauth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCeremonyOldestReplacementPreservesPeerAndOneUseBounds(t *testing.T) {
	now := time.Now()
	c := NewCeremonies(func() time.Time { return now }, nil)
	var pending []Ceremony
	for i := 0; i < MaxCeremoniesPerPeer; i++ {
		created, err := c.CreateReplacingOldest(CeremonyLogin, "192.0.2.1:1", "", "", "", nil, webauthnSession())
		if err != nil {
			t.Fatal(err)
		}
		pending = append(pending, created)
		now = now.Add(time.Millisecond)
	}
	other, err := c.Create(CeremonyLogin, "192.0.2.2:1", "", "", "", nil, webauthnSession())
	if err != nil {
		t.Fatal(err)
	}
	// Failure to generate a replacement must not evict a valid challenge.
	random := c.random
	c.random = strings.NewReader("")
	if _, err := c.CreateReplacingOldest(CeremonyLogin, "192.0.2.1:2", "", "", "", nil, webauthnSession()); err == nil {
		t.Fatal("broken randomness accepted")
	}
	if len(c.records) != MaxCeremoniesPerPeer+1 {
		t.Fatal("failed creation evicted a challenge")
	}
	c.random = random
	replacement, err := c.CreateReplacingOldest(CeremonyLogin, "192.0.2.1:2", "", "", "", nil, webauthnSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.records) != MaxCeremoniesPerPeer+1 || c.peerCounts["192.0.2.1"] != MaxCeremoniesPerPeer {
		t.Fatal("replacement grew bounds")
	}
	if _, err := c.Consume(pending[0].CookieToken, CeremonyLogin, "", ""); !errors.Is(err, ErrInvalidCeremony) {
		t.Fatal("oldest challenge was not evicted")
	}
	for _, ceremony := range append(pending[1:], other, replacement) {
		if _, err := c.Consume(ceremony.CookieToken, CeremonyReauth, "", ""); !errors.Is(err, ErrInvalidCeremony) {
			t.Fatal("replacement lost kind binding")
		}
		if _, err := c.Consume(ceremony.CookieToken, CeremonyLogin, "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Consume(ceremony.CookieToken, CeremonyLogin, "", ""); !errors.Is(err, ErrInvalidCeremony) {
			t.Fatal("challenge replay accepted")
		}
	}
	if len(c.records) != 0 || len(c.peerCounts) != 0 {
		t.Fatal("replacement leaked capacity")
	}
}

func TestCeremonyOldestGlobalReplacementKeepsDefaultPolicy(t *testing.T) {
	now := time.Now()
	c := NewCeremonies(func() time.Time { return now }, nil)
	var first Ceremony
	for i := 0; i < MaxCeremonies; i++ {
		created, err := c.Create(CeremonyLogin, fmt.Sprintf("peer-%d", i), "", "", "", nil, webauthnSession())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = created
		}
		now = now.Add(time.Millisecond)
	}
	if _, err := c.Create(CeremonyLogin, "new-peer", "", "", "", nil, webauthnSession()); !errors.Is(err, ErrCapacity) {
		t.Fatal("default capacity policy changed")
	}
	replacement, err := c.CreateReplacingOldest(CeremonyLogin, "new-peer", "", "", "", nil, webauthnSession())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.records) != MaxCeremonies || len(c.peerCounts) != MaxCeremonies || c.peerCounts[first.Peer] != 0 {
		t.Fatal("global replacement lost bounded accounting")
	}
	if _, err := c.Consume(first.CookieToken, CeremonyLogin, "", ""); !errors.Is(err, ErrInvalidCeremony) {
		t.Fatal("oldest global challenge survived")
	}
	now = now.Add(CeremonyLifetime)
	if _, err := c.Consume(replacement.CookieToken, CeremonyLogin, "", ""); !errors.Is(err, ErrInvalidCeremony) {
		t.Fatal("replacement ignored expiry")
	}
}
