package searchrank

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// Compare the token stream, including order and repetitions, with the actual
// tokenizer used by messages_fts. This catches drift on SQLite/Unicode upgrades.
func TestTitleTokensMatchFTS5(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE ft USING fts5(body); CREATE VIRTUAL TABLE vocab USING fts5vocab(ft,instance)`); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		"CAFÉ café cafe\u0301 • RÉSUMÉ coöperate Straße STRASSE",
		"ấ Ấ a\u0302\u0301 Ł Ø Æ ǽ ß ẞ İ ı ſ ﬆ ﬁ", // not wholesale ASCII folding or NFKD
		"Σςσ Ά ά α Μ µ Ι ι ι ΐ ΰ й Й सिता \ue000 杭州",
		"A🫠B A😀B AᢅB AᦱB AᳲB A\uffffB", // unassigned vs assigned in Unicode 6.1
		" \u0301!src/MAIN.go\"\x00OR * - (x) abc123!!!",
	} {
		res, err := db.Exec(`INSERT INTO ft VALUES(?)`, input)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`SELECT term FROM vocab WHERE doc=? ORDER BY offset`, id)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for rows.Next() {
			var word string
			if err := rows.Scan(&word); err != nil {
				t.Fatal(err)
			}
			want = append(want, word)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if got := searchTitleTokens(input); !slices.Equal(got, want) {
			t.Errorf("tokens for %q = %q; FTS5 = %q", input, got, want)
		}
	}
}

func TestParseDistinctCoverage(t *testing.T) {
	q := Parse("cafe CAFÉ cafe\u0301 missing", false)
	if len(q.terms) != 2 || q.minimum() != 2 || q.Highlight != `"cafe" OR "missing"` {
		t.Fatalf("inflated coverage or MATCH expression: %+v", q)
	}
	for _, tc := range []struct {
		title    string
		coverage uint64
		phrase   bool
	}{
		{"Café", 1, false}, {"cafe\u0301 missing", 3, true}, {"missing café", 3, false}, {"cafeteria", 0, false},
	} {
		if mask, phrase := q.titleMatch(tc.title); mask != tc.coverage || phrase != tc.phrase {
			t.Errorf("%q: got (%b,%v), want (%b,%v)", tc.title, mask, phrase, tc.coverage, tc.phrase)
		}
	}
	// A whitespace-delimited literal stays a phrase, never two independent hits.
	path := Parse(`src/main.go SRC-MAIN.GO`, false)
	if path.Highlight != `"src main go"` {
		t.Fatal(path.Highlight)
	}
	if bits, _ := path.titleMatch("src another main go"); bits != 0 {
		t.Fatal("matched nonadjacent path tokens")
	}
}

func TestPrefixRequiresOptInAndUnfinishedWord(t *testing.T) {
	for _, tc := range []struct {
		query         string
		allow, prefix bool
	}{
		{"design form", false, false}, {"design form", true, true}, {"design form\t", true, false},
		{"design form.", true, false}, {"design form*", true, false}, {"design src/form", true, false},
		{"design 1234", true, false}, {"design abc", true, false}, {"form", true, false}, {"design cafe\u0301", true, true},
		{"form design FORM", true, false}, {"a b c d e f g h i j k l form", true, false},
	} {
		q := Parse(tc.query, tc.allow)
		if q.prefix != tc.prefix || strings.HasSuffix(q.Highlight, "*") != tc.prefix {
			t.Errorf("Parse(%q,%v): prefix %v, MATCH %q", tc.query, tc.allow, q.prefix, q.Highlight)
		}
	}
	for _, allow := range []bool{false, true} {
		q := Parse("design form", allow)
		bits, phrase := q.titleMatch("Design formatter")
		if (bits == 3) != allow || phrase != allow {
			t.Errorf("prefix %v: coverage=%b phrase=%v", allow, bits, phrase)
		}
	}
}
