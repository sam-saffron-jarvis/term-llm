package main

import (
	"fmt"
	"os"

	"github.com/term-llm/term-llm/cmd"
)

// Only the opt-in prototype build replaces this no-op dispatcher.
var dispatchProxy = func([]string) error { return nil }

func main() {
	if err := dispatchProxy(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cannot launch embedded Rust proxy:", err)
		os.Exit(1)
	}
	cmd.Execute()
}
