package branchtree

import "testing"

func TestTurnEnd(t *testing.T) {
	type row struct {
		seq  int
		tool bool
	}
	for _, tc := range []struct {
		name   string
		anchor int
		rows   []row
		want   int
	}{
		{"final answer followed by user", 1, []row{{0, false}, {1, false}, {2, false}}, 1},
		{"trailing tool rows join the turn", 1, []row{{0, false}, {1, false}, {2, true}, {3, true}, {4, false}, {5, true}}, 3},
		{"tools at end of transcript", 1, []row{{0, false}, {1, false}, {2, true}}, 2},
		{"unordered input", 1, []row{{4, false}, {3, true}, {1, false}, {2, true}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TurnEnd(tc.anchor, func(yield func(int, bool) bool) {
				for _, r := range tc.rows {
					if !yield(r.seq, r.tool) {
						return
					}
				}
			})
			if got != tc.want {
				t.Fatalf("TurnEnd = %d, want %d", got, tc.want)
			}
		})
	}
}
