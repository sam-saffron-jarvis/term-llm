package chat

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	dataHome, err := os.MkdirTemp("", "term-llm-chat-test-data-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp XDG_DATA_HOME: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_DATA_HOME", dataHome); err != nil {
		fmt.Fprintf(os.Stderr, "set XDG_DATA_HOME: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dataHome)
	os.Exit(code)
}
