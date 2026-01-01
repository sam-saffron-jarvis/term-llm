package cmd

import "testing"

func TestEnvEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "one", value: "1", want: true},
		{name: "true", value: "true", want: true},
		{name: "true-caps", value: "TRUE", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "y", value: "y", want: true},
		{name: "spaced", value: "  yes  ", want: true},
		{name: "zero", value: "0", want: false},
		{name: "false", value: "false", want: false},
		{name: "no", value: "no", want: false},
		{name: "garbage", value: "nope", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(allowAutoRunEnv, tt.value)
			if got := envEnabled(allowAutoRunEnv); got != tt.want {
				t.Fatalf("envEnabled(%q)=%v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
