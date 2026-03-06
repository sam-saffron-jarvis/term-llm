package llm

import (
	"testing"
	"time"
)

func TestRateLimitErrorRetryWindow(t *testing.T) {
	withinWindow := &RateLimitError{RetryAfter: maxAutomaticRateLimitRetryAfter - time.Second}
	if withinWindow.IsLongWait() {
		t.Fatalf("expected %s wait to be retryable", withinWindow.RetryAfter)
	}
	if !isRetryable(withinWindow) {
		t.Fatalf("expected %s wait to be retryable", withinWindow.RetryAfter)
	}

	overWindow := &RateLimitError{RetryAfter: maxAutomaticRateLimitRetryAfter + time.Second}
	if !overWindow.IsLongWait() {
		t.Fatalf("expected %s wait to be treated as too long", overWindow.RetryAfter)
	}
	if isRetryable(overWindow) {
		t.Fatalf("expected %s wait to skip automatic retry", overWindow.RetryAfter)
	}
}

func TestRetryProviderCalculateBackoffHonorsRateLimitRetryAfter(t *testing.T) {
	provider := &RetryProvider{config: DefaultRetryConfig()}
	retryAfter := 10*time.Minute + 11*time.Second

	got := provider.calculateBackoff(1, &RateLimitError{RetryAfter: retryAfter})
	if got != retryAfter {
		t.Fatalf("calculateBackoff() = %s, want %s", got, retryAfter)
	}
}
