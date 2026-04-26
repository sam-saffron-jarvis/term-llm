package contain

import (
	"context"
	"testing"
)

func TestListReconciliation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeComposeForDockerTest(t, "missing", "")
	writeComposeForDockerTest(t, "running", "")
	writeComposeForDockerTest(t, "stopped", "")
	writeComposeForDockerTest(t, "bad", "services: [")

	r := &fakeRunner{output: []byte(`{"Names":"running_1","State":"running","Status":"Up 1 minute","Labels":{"org.term-llm.contain":"true","org.term-llm.contain.name":"running","org.term-llm.contain.service":"app"}}
{"Names":"stopped_1","State":"exited","Status":"Exited (0)","Labels":"org.term-llm.contain=true,org.term-llm.contain.name=stopped,org.term-llm.contain.service=app"}
{"Names":"scratch_1","State":"running","Status":"Up","Labels":{"org.term-llm.contain":"true","org.term-llm.contain.name":"scratch","org.term-llm.contain.config_dir":"/old/path","org.term-llm.contain.service":"app"}}
`)}
	entries, err := List(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ListEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	checks := map[string]string{
		"missing": "missing",
		"running": "running",
		"stopped": "stopped",
		"bad":     "invalid",
		"scratch": "orphaned",
	}
	for name, status := range checks {
		entry, ok := got[name]
		if !ok {
			t.Fatalf("missing entry %q in %+v", name, entries)
		}
		if entry.Status != status {
			t.Fatalf("entry %q status = %q, want %q", name, entry.Status, status)
		}
	}
	if got["bad"].Service != "-" {
		t.Fatalf("invalid service = %q", got["bad"].Service)
	}
	if got["scratch"].ConfigDir != "/old/path" {
		t.Fatalf("orphan config dir = %q", got["scratch"].ConfigDir)
	}
}
