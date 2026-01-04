package cmd

import "testing"

func TestParseFileSpec(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantPath  string
		wantStart int
		wantEnd   int
		wantGuard bool
		wantErr   bool
	}{
		{name: "no guard", input: "main.go", wantPath: "main.go"},
		{name: "range", input: "main.go:11-22", wantPath: "main.go", wantStart: 11, wantEnd: 22, wantGuard: true},
		{name: "start only", input: "main.go:11-", wantPath: "main.go", wantStart: 11, wantEnd: 0, wantGuard: true},
		{name: "end only", input: "main.go:-22", wantPath: "main.go", wantStart: 0, wantEnd: 22, wantGuard: true},
		{name: "invalid", input: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := parseFileSpec(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec.Path != tc.wantPath {
				t.Fatalf("path=%q, want %q", spec.Path, tc.wantPath)
			}
			if spec.StartLine != tc.wantStart || spec.EndLine != tc.wantEnd || spec.HasGuard != tc.wantGuard {
				t.Fatalf("got start=%d end=%d guard=%v, want start=%d end=%d guard=%v",
					spec.StartLine, spec.EndLine, spec.HasGuard, tc.wantStart, tc.wantEnd, tc.wantGuard)
			}
		})
	}
}
