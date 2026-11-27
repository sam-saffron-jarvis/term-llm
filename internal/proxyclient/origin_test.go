package proxyclient

import (
	"fmt"
	"sync/atomic"
	"testing"
)

var canonicalTestSerial atomic.Uint64

func TestConflictingCanonicalRootsFailAtomically(t *testing.T) {
	suffix := fmt.Sprintf("/chatgpt-%d", canonicalTestSerial.Add(1))
	root := "https://conflict-canonical.example.test" + suffix
	equivalent := "wss://CONFLICT-CANONICAL.example.test:443" + suffix + "/"
	err := Register([]Binding{{Root: root, Kind: "chatgpt", Token: "first"}, {Root: equivalent, Kind: "", Token: "first"}})
	if err == nil {
		t.Fatal("credential-kind collision could fall back to a local vendor login")
	}
	if _, ok := Lookup(root); ok {
		t.Fatal("failed registration partially published")
	}
	if err := Register([]Binding{{Root: root, Kind: "chatgpt", Token: "first"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup(equivalent + "responses"); !ok {
		t.Fatal("equivalent authority lost delegation")
	}
	if !SameRoot(root, "https://CONFLICT-CANONICAL.example.test:443"+suffix+"/") || SameRoot(root, root+"/nested") {
		t.Fatal("root equivalence widened or narrowed incorrectly")
	}
}

func TestLiveRootCannotSilentlyBorrowAnotherCallerToken(t *testing.T) {
	root := "https://principal-snapshot.example.test/ollama"
	if err := Register([]Binding{{Root: root, Token: "original-caller"}}); err != nil {
		t.Fatal(err)
	}
	err := Register([]Binding{{Root: root + "-new", Token: "another-caller"}, {Root: root, Token: "another-caller"}})
	if err == nil {
		t.Fatal("a live CLI/public-model/Ollama binding silently changed principals")
	}
	if got, ok := Lookup(root + "/api/chat"); !ok || got.Token != "original-caller" {
		t.Fatal("existing caller identity was overwritten")
	}
	if _, ok := Lookup(root + "-new/models"); ok {
		t.Fatal("failed principal change partially published another root")
	}
}

func TestCanonicalRootMatchesSameRoot(t *testing.T) {
	left, err := CanonicalRoot("https://Proxy.Example.test:443/claude-bin/")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalRoot("https://proxy.example.test/claude-bin")
	if err != nil || left != right {
		t.Fatalf("canonical roots %q and %q differ: %v", left, right, err)
	}
	if other, _ := CanonicalRoot("https://proxy.example.test/claude-bin/nested"); other == left {
		t.Fatal("child path has the same canonical root")
	}
	if _, err := CanonicalRoot("https://proxy.example.test/root?query"); err == nil {
		t.Fatal("accepted a root with a query")
	}
}
