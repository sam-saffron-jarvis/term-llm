package sqlitefts

import "testing"

func TestLiteralQueryEscapesFTS5Syntax(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"term-llm", `"term-llm"`},
		{"term-llm architecture", `"term-llm" "architecture"`},
		{`foo "bar`, `"foo" """bar"`},
		{"OR NOT NEAR", `"OR" "NOT" "NEAR"`},
		{"שלום עולם", `"שלום" "עולם"`},
	}
	for _, tt := range tests {
		if got := LiteralQuery(tt.query); got != tt.want {
			t.Fatalf("LiteralQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestLiteralQueryMinDedupesCaseInsensitively(t *testing.T) {
	got := LiteralQueryMin("a an AN term", 2)
	want := `"an" "term"`
	if got != want {
		t.Fatalf("LiteralQueryMin() = %q, want %q", got, want)
	}
}

func TestPrefixORQuery(t *testing.T) {
	got := PrefixORQuery("AI: memory, memory-search NOT", 3)
	want := `"memory"* OR "memorysearch"* OR "not"*`
	if got != want {
		t.Fatalf("PrefixORQuery() = %q, want %q", got, want)
	}
}
