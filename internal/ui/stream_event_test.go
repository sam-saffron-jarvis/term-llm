package ui

import "testing"

func TestFormatRetryStatusWithUnknownMax(t *testing.T) {
	got := FormatRetryStatus("Retrying", 3, 0, 1.25, 1, "...")
	want := "Retrying (attempt 3), waiting 1.2s..."
	if got != want {
		t.Fatalf("FormatRetryStatus = %q, want %q", got, want)
	}
}

func TestFormatRetryStatusWithMax(t *testing.T) {
	got := FormatRetryStatus("Rate limited", 2, 5, 3.5, 0, "...")
	want := "Rate limited (2/5), waiting 4s..."
	if got != want {
		t.Fatalf("FormatRetryStatus = %q, want %q", got, want)
	}
}
