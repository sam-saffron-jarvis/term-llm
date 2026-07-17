package credentials

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/oauth"
)

func TestRefreshChatGPTCredentialsConcurrentCallersReuseRotatedCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	stale := &ChatGPTCredentials{
		AccessToken:  "stale-access",
		RefreshToken: "stale-refresh",
		ExpiresAt:    time.Now().Add(-time.Minute).Unix(),
		AccountID:    "account-1",
	}
	if err := SaveChatGPTCredentials(stale); err != nil {
		t.Fatalf("save stale credentials: %v", err)
	}
	first, err := GetChatGPTCredentials()
	if err != nil {
		t.Fatalf("load first credential copy: %v", err)
	}
	second, err := GetChatGPTCredentials()
	if err != nil {
		t.Fatalf("load second credential copy: %v", err)
	}

	originalRefresh := refreshChatGPTToken
	defer func() { refreshChatGPTToken = originalRefresh }()

	var calls atomic.Int32
	exchangeStarted := make(chan struct{})
	releaseExchange := make(chan struct{})
	refreshChatGPTToken = func(refreshToken string) (*oauth.ChatGPTTokenResponse, error) {
		if refreshToken != "stale-refresh" {
			return nil, errors.New("unexpected refresh token")
		}
		if calls.Add(1) != 1 {
			return nil, errors.New("stale refresh token was exchanged twice")
		}
		close(exchangeStarted)
		<-releaseExchange
		return &oauth.ChatGPTTokenResponse{
			AccessToken:  "renewed-access",
			RefreshToken: "rotated-refresh",
			ExpiresIn:    3600,
		}, nil
	}

	results := make(chan error, 2)
	go func() { results <- RefreshChatGPTCredentials(first) }()
	<-exchangeStarted
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		results <- RefreshChatGPTCredentials(second)
	}()
	<-secondStarted
	close(releaseExchange)

	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent refresh failed: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh exchanges = %d, want 1", got)
	}
	for name, creds := range map[string]*ChatGPTCredentials{"first": first, "second": second} {
		if creds.AccessToken != "renewed-access" || creds.RefreshToken != "rotated-refresh" {
			t.Fatalf("%s credentials were not renewed: %#v", name, creds)
		}
	}

	stored, err := GetChatGPTCredentials()
	if err != nil {
		t.Fatalf("load renewed credentials: %v", err)
	}
	if stored.AccessToken != "renewed-access" || stored.RefreshToken != "rotated-refresh" {
		t.Fatalf("stored credentials were not renewed: %#v", stored)
	}
}
