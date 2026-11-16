package proxy

import (
	"strings"
	"testing"
	"time"
)

func TestAccountExpiryUsesUnixSecondsNotBrowserMilliseconds(t *testing.T) {
	creds := accountCredentials{AccessToken: "access-canary", RefreshToken: "refresh-canary", AccountID: "id", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := validateAccountCredentials("chatgpt", creds); err != nil {
		t.Fatal(err)
	}
	creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	err := validateAccountCredentials("chatgpt", creds)
	if err == nil || !strings.Contains(err.Error(), "seconds") || strings.Contains(err.Error(), "canary") {
		t.Fatal("millisecond expiry accepted or secret leaked")
	}
}
