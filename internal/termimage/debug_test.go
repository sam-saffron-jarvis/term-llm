package termimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugfWritesDebugFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image-debug.log")
	Debugf(Environment{Debug: true, DebugFile: path}, "hello %s", "world")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read debug file: %v", err)
	}
	if got := string(data); !strings.Contains(got, "[term-llm image] hello world") {
		t.Fatalf("debug file = %q", got)
	}
}
