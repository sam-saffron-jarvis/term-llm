package edit

import (
	"testing"
)

func TestInlineEditParser_Insert(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantEdits []InlineEdit
		wantText  string
	}{
		{
			name:  "simple insert",
			input: `<INSERT after="anchor">new line</INSERT>`,
			wantEdits: []InlineEdit{
				{Type: InlineEditInsert, After: "anchor", Content: []string{"new line"}},
			},
			wantText: "",
		},
		{
			name:  "insert without after",
			input: `<INSERT>new line</INSERT>`,
			wantEdits: []InlineEdit{
				{Type: InlineEditInsert, After: "", Content: []string{"new line"}},
			},
			wantText: "",
		},
		{
			name: "multiline insert",
			input: `<INSERT after="anchor">
line 1
line 2
line 3
</INSERT>`,
			wantEdits: []InlineEdit{
				{Type: InlineEditInsert, After: "anchor", Content: []string{"line 1", "line 2", "line 3"}},
			},
			wantText: "",
		},
		{
			name:  "text before and after",
			input: `Some text before <INSERT after="anchor">content</INSERT> and after`,
			wantEdits: []InlineEdit{
				{Type: InlineEditInsert, After: "anchor", Content: []string{"content"}},
			},
			wantText: "Some text before  and after",
		},
		{
			name:  "case insensitive",
			input: `<insert after="test">content</insert>`,
			wantEdits: []InlineEdit{
				{Type: InlineEditInsert, After: "test", Content: []string{"content"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotEdits []InlineEdit
			var gotText string

			p := NewInlineEditParser()
			p.OnEdit = func(edit InlineEdit) {
				gotEdits = append(gotEdits, edit)
			}
			p.OnText = func(text string) {
				gotText += text
			}

			p.Feed(tt.input)
			p.Flush()

			if len(gotEdits) != len(tt.wantEdits) {
				t.Errorf("got %d edits, want %d", len(gotEdits), len(tt.wantEdits))
				return
			}

			for i, got := range gotEdits {
				want := tt.wantEdits[i]
				if got.Type != want.Type {
					t.Errorf("edit[%d].Type = %v, want %v", i, got.Type, want.Type)
				}
				if got.After != want.After {
					t.Errorf("edit[%d].After = %q, want %q", i, got.After, want.After)
				}
				if len(got.Content) != len(want.Content) {
					t.Errorf("edit[%d].Content = %v, want %v", i, got.Content, want.Content)
				} else {
					for j, line := range got.Content {
						if line != want.Content[j] {
							t.Errorf("edit[%d].Content[%d] = %q, want %q", i, j, line, want.Content[j])
						}
					}
				}
			}

			if tt.wantText != "" && gotText != tt.wantText {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
		})
	}
}

func TestInlineEditParser_Delete(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantEdits []InlineEdit
		wantText  string
	}{
		{
			name:  "single line delete",
			input: `<DELETE from="line to remove" />`,
			wantEdits: []InlineEdit{
				{Type: InlineEditDelete, From: "line to remove", To: ""},
			},
		},
		{
			name:  "range delete",
			input: `<DELETE from="start line" to="end line" />`,
			wantEdits: []InlineEdit{
				{Type: InlineEditDelete, From: "start line", To: "end line"},
			},
		},
		{
			name:  "multiple deletes",
			input: `<DELETE from="first" /><DELETE from="second" />`,
			wantEdits: []InlineEdit{
				{Type: InlineEditDelete, From: "first", To: ""},
				{Type: InlineEditDelete, From: "second", To: ""},
			},
		},
		{
			name:  "case insensitive",
			input: `<delete from="test" />`,
			wantEdits: []InlineEdit{
				{Type: InlineEditDelete, From: "test", To: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotEdits []InlineEdit

			p := NewInlineEditParser()
			p.OnEdit = func(edit InlineEdit) {
				gotEdits = append(gotEdits, edit)
			}

			p.Feed(tt.input)
			p.Flush()

			if len(gotEdits) != len(tt.wantEdits) {
				t.Errorf("got %d edits, want %d", len(gotEdits), len(tt.wantEdits))
				return
			}

			for i, got := range gotEdits {
				want := tt.wantEdits[i]
				if got.Type != want.Type {
					t.Errorf("edit[%d].Type = %v, want %v", i, got.Type, want.Type)
				}
				if got.From != want.From {
					t.Errorf("edit[%d].From = %q, want %q", i, got.From, want.From)
				}
				if got.To != want.To {
					t.Errorf("edit[%d].To = %q, want %q", i, got.To, want.To)
				}
			}
		})
	}
}

func TestInlineEditParser_Streaming(t *testing.T) {
	// Test that partial chunks are handled correctly
	t.Run("insert split across chunks", func(t *testing.T) {
		var gotEdits []InlineEdit

		p := NewInlineEditParser()
		p.OnEdit = func(edit InlineEdit) {
			gotEdits = append(gotEdits, edit)
		}

		// Feed in small chunks
		chunks := []string{
			"<INS",
			"ERT after=\"",
			"anchor\">cont",
			"ent</INSERT>",
		}

		for _, chunk := range chunks {
			p.Feed(chunk)
		}
		p.Flush()

		if len(gotEdits) != 1 {
			t.Fatalf("got %d edits, want 1", len(gotEdits))
		}

		if gotEdits[0].After != "anchor" {
			t.Errorf("After = %q, want %q", gotEdits[0].After, "anchor")
		}
		if len(gotEdits[0].Content) != 1 || gotEdits[0].Content[0] != "content" {
			t.Errorf("Content = %v, want [content]", gotEdits[0].Content)
		}
	})

	t.Run("delete split across chunks", func(t *testing.T) {
		var gotEdits []InlineEdit

		p := NewInlineEditParser()
		p.OnEdit = func(edit InlineEdit) {
			gotEdits = append(gotEdits, edit)
		}

		chunks := []string{
			"<DEL",
			"ETE from=\"",
			"test\" />",
		}

		for _, chunk := range chunks {
			p.Feed(chunk)
		}
		p.Flush()

		if len(gotEdits) != 1 {
			t.Fatalf("got %d edits, want 1", len(gotEdits))
		}

		if gotEdits[0].From != "test" {
			t.Errorf("From = %q, want %q", gotEdits[0].From, "test")
		}
	})
}

func TestInlineEditParser_Mixed(t *testing.T) {
	input := `Here is some text.
<INSERT after="text">
- new bullet
- another bullet
</INSERT>
More text here.
<DELETE from="old line" />
Final text.`

	var gotEdits []InlineEdit
	var gotText string

	p := NewInlineEditParser()
	p.OnEdit = func(edit InlineEdit) {
		gotEdits = append(gotEdits, edit)
	}
	p.OnText = func(text string) {
		gotText += text
	}

	p.Feed(input)
	p.Flush()

	if len(gotEdits) != 2 {
		t.Fatalf("got %d edits, want 2", len(gotEdits))
	}

	// First edit should be INSERT
	if gotEdits[0].Type != InlineEditInsert {
		t.Errorf("edit[0].Type = %v, want INSERT", gotEdits[0].Type)
	}
	if gotEdits[0].After != "text" {
		t.Errorf("edit[0].After = %q, want %q", gotEdits[0].After, "text")
	}
	if len(gotEdits[0].Content) != 2 {
		t.Errorf("edit[0].Content has %d lines, want 2", len(gotEdits[0].Content))
	}

	// Second edit should be DELETE
	if gotEdits[1].Type != InlineEditDelete {
		t.Errorf("edit[1].Type = %v, want DELETE", gotEdits[1].Type)
	}
	if gotEdits[1].From != "old line" {
		t.Errorf("edit[1].From = %q, want %q", gotEdits[1].From, "old line")
	}

	// Check that text outside markers was captured
	if !contains(gotText, "Here is some text") {
		t.Errorf("text missing 'Here is some text': %q", gotText)
	}
	if !contains(gotText, "More text here") {
		t.Errorf("text missing 'More text here': %q", gotText)
	}
	if !contains(gotText, "Final text") {
		t.Errorf("text missing 'Final text': %q", gotText)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestInlineEditParser_PartialInsert(t *testing.T) {
	t.Run("streams lines as they arrive", func(t *testing.T) {
		var gotEdits []InlineEdit
		var partialLines []string
		var partialAfter string

		p := NewInlineEditParser()
		p.OnEdit = func(edit InlineEdit) {
			gotEdits = append(gotEdits, edit)
		}
		p.OnPartialInsert = func(after string, line string) {
			partialAfter = after
			partialLines = append(partialLines, line)
		}

		// Feed content with newlines to trigger partial emits
		p.Feed(`<INSERT after="anchor">`)
		p.Feed("line 1\n")
		if len(partialLines) != 1 || partialLines[0] != "line 1" {
			t.Errorf("after first newline: got %v, want [line 1]", partialLines)
		}

		p.Feed("line 2\nline 3\n")
		if len(partialLines) != 3 {
			t.Errorf("after more newlines: got %d lines, want 3", len(partialLines))
		}
		if partialLines[1] != "line 2" || partialLines[2] != "line 3" {
			t.Errorf("lines = %v, want [line 1, line 2, line 3]", partialLines)
		}

		if partialAfter != "anchor" {
			t.Errorf("partialAfter = %q, want %q", partialAfter, "anchor")
		}

		p.Feed("</INSERT>")
		p.Flush()

		// OnEdit should still be called with all content
		if len(gotEdits) != 1 {
			t.Fatalf("got %d edits, want 1", len(gotEdits))
		}
		if len(gotEdits[0].Content) != 3 {
			t.Errorf("edit content has %d lines, want 3", len(gotEdits[0].Content))
		}
	})

	t.Run("handles content without trailing newline", func(t *testing.T) {
		var partialLines []string

		p := NewInlineEditParser()
		p.OnPartialInsert = func(after string, line string) {
			partialLines = append(partialLines, line)
		}

		p.Feed(`<INSERT after="test">line 1`)
		// No partial emit yet - no complete line
		if len(partialLines) != 0 {
			t.Errorf("got %d partial lines before newline, want 0", len(partialLines))
		}

		p.Feed("\nline 2</INSERT>")
		// Now line 1 should be emitted
		if len(partialLines) != 1 || partialLines[0] != "line 1" {
			t.Errorf("partial lines = %v, want [line 1]", partialLines)
		}

		p.Flush()
	})

	t.Run("handles split chunks", func(t *testing.T) {
		var partialLines []string

		p := NewInlineEditParser()
		p.OnPartialInsert = func(after string, line string) {
			partialLines = append(partialLines, line)
		}

		// Simulate realistic streaming chunks
		chunks := []string{
			"<INSERT after=\"",
			"anchor\">first ",
			"line\nsecond ",
			"line\nthird line\n",
			"</INSERT>",
		}

		for _, chunk := range chunks {
			p.Feed(chunk)
		}
		p.Flush()

		// Should have emitted all three lines
		if len(partialLines) != 3 {
			t.Errorf("got %d partial lines, want 3: %v", len(partialLines), partialLines)
		}
		expected := []string{"first line", "second line", "third line"}
		for i, want := range expected {
			if i < len(partialLines) && partialLines[i] != want {
				t.Errorf("partialLines[%d] = %q, want %q", i, partialLines[i], want)
			}
		}
	})

	t.Run("skips leading empty line", func(t *testing.T) {
		var partialLines []string

		p := NewInlineEditParser()
		p.OnPartialInsert = func(after string, line string) {
			partialLines = append(partialLines, line)
		}

		// INSERT with leading newline (common pattern)
		p.Feed("<INSERT after=\"anchor\">\nline 1\nline 2\n</INSERT>")
		p.Flush()

		// Should not include empty first line
		if len(partialLines) != 2 {
			t.Errorf("got %d partial lines, want 2: %v", len(partialLines), partialLines)
		}
		if len(partialLines) >= 2 && (partialLines[0] != "line 1" || partialLines[1] != "line 2") {
			t.Errorf("partial lines = %v, want [line 1, line 2]", partialLines)
		}
	})

	t.Run("no callback set", func(t *testing.T) {
		var gotEdits []InlineEdit

		p := NewInlineEditParser()
		p.OnEdit = func(edit InlineEdit) {
			gotEdits = append(gotEdits, edit)
		}
		// OnPartialInsert not set

		p.Feed("<INSERT after=\"test\">\nline 1\nline 2\n</INSERT>")
		p.Flush()

		// Should still work, just no partial callbacks
		if len(gotEdits) != 1 {
			t.Fatalf("got %d edits, want 1", len(gotEdits))
		}
		if len(gotEdits[0].Content) != 2 {
			t.Errorf("edit content has %d lines, want 2", len(gotEdits[0].Content))
		}
	})
}
