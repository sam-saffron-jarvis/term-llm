package pprof

import "testing"

func TestResolvePort(t *testing.T) {
	for _, tt := range []struct {
		flag, env string
		want      int
		bad       bool
	}{{"", "", -1, false}, {"0", "", 0, false}, {"1234", "bad", 1234, false}, {"", "true", 0, false}, {"", "1", 0, false}, {"", "4321", 4321, false}, {"-1", "", 0, true}, {"65536", "", 0, true}, {"", "invalid", 0, true}} {
		got, err := ResolvePort(tt.flag, tt.env)
		if (err != nil) != tt.bad || !tt.bad && got != tt.want {
			t.Errorf("ResolvePort(%q,%q)=%d,%v", tt.flag, tt.env, got, err)
		}
	}
}
