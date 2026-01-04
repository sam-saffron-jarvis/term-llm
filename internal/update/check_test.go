package update

import "testing"

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trim v prefix", input: "v1.2.3", want: "1.2.3"},
		{name: "strip suffix", input: "1.2.3-beta1", want: "1.2.3"},
		{name: "whitespace", input: "  v2.0  ", want: "2.0"},
		{name: "non-numeric", input: "dev", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeVersion(tc.input); got != tc.want {
				t.Fatalf("NormalizeVersion(%q)=%q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCompareVersionStrings(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		wantCmp  int
		wantOkay bool
	}{
		{name: "equal different lengths", a: "1.2", b: "1.2.0", wantCmp: 0, wantOkay: true},
		{name: "less than", a: "1.2.3", b: "1.10.0", wantCmp: -1, wantOkay: true},
		{name: "greater than", a: "2.0", b: "1.9.9", wantCmp: 1, wantOkay: true},
		{name: "invalid", a: "1.a", b: "1.2.3", wantCmp: 0, wantOkay: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmp, ok := CompareVersionStrings(tc.a, tc.b)
			if ok != tc.wantOkay {
				t.Fatalf("CompareVersionStrings(%q,%q) ok=%v, want %v", tc.a, tc.b, ok, tc.wantOkay)
			}
			if !ok {
				return
			}
			if cmp != tc.wantCmp {
				t.Fatalf("CompareVersionStrings(%q,%q)=%d, want %d", tc.a, tc.b, cmp, tc.wantCmp)
			}
		})
	}
}
