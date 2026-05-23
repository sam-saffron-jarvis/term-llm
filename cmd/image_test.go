package cmd

import (
	"strings"
	"testing"
)

func TestImageSpinnerEnvironmentDisablesBubbleTeaModeQueries(t *testing.T) {
	env := imageSpinnerEnvironment([]string{
		"TERM=xterm-kitty",
		"TERM_PROGRAM=WezTerm",
		"WT_SESSION=abc",
		"KITTY_WINDOW_ID=1",
		"PATH=/bin",
	})

	values := map[string]string{}
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		values[name] = value
	}
	if values["TERM"] != "xterm-256color" {
		t.Fatalf("TERM = %q, want xterm-256color (env: %#v)", values["TERM"], env)
	}
	if values["TERM_PROGRAM"] != "Apple_Terminal" {
		t.Fatalf("TERM_PROGRAM = %q, want Apple_Terminal (env: %#v)", values["TERM_PROGRAM"], env)
	}
	if _, ok := values["WT_SESSION"]; ok {
		t.Fatalf("WT_SESSION should be removed: %#v", env)
	}
	if values["KITTY_WINDOW_ID"] != "1" || values["PATH"] != "/bin" {
		t.Fatalf("unrelated env should be preserved: %#v", env)
	}
}
