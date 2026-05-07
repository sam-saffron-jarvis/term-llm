package update

import "testing"

func TestUpgradeSuccessMessageIncludesVersionTransition(t *testing.T) {
	got := upgradeSuccessMessage("0.0.200", "v0.0.213", "/home/discourse/.local/bin/term-llm")
	want := "term-llm upgraded v0.0.200 -> v0.0.213 at /home/discourse/.local/bin/term-llm\n"
	if got != want {
		t.Fatalf("upgradeSuccessMessage() = %q, want %q", got, want)
	}
}

func TestUpgradeSuccessMessagePreservesDevVersion(t *testing.T) {
	got := upgradeSuccessMessage("dev", "v0.0.213", "/tmp/term-llm")
	want := "term-llm upgraded dev -> v0.0.213 at /tmp/term-llm\n"
	if got != want {
		t.Fatalf("upgradeSuccessMessage() = %q, want %q", got, want)
	}
}
