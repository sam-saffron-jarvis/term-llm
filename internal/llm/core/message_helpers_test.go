package core

import (
	"reflect"
	"testing"
)

func TestInsertRequestContextPreservesUserTurnAnchor(t *testing.T) {
	developer := Message{Role: RoleDeveloper, Parts: []Part{{Type: PartText, Text: "leading instructions"}}}
	context := Message{Role: RoleDeveloper, Parts: []Part{{Type: PartText, Text: "skill instructions"}}}
	original := []Message{SystemText("system"), developer, UserText("first"), AssistantText("reply"), UserText("second")}
	for _, test := range []struct{ turn, index int }{{0, 2}, {1, 2}, {2, 4}, {9, 2}} {
		input := make([]Message, len(original), len(original)+2)
		copy(input, original)
		got := InsertRequestContext(input, context, test.turn)
		want := append(append(append([]Message(nil), original[:test.index]...), context), original[test.index:]...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("turn %d: got %#v, want %#v", test.turn, got, want)
		}
		if !reflect.DeepEqual(input, original) {
			t.Fatalf("turn %d: context insertion mutated the caller's history", test.turn)
		}
	}
}
