package searchrank

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/unicode/rangetable"
)

var assigned61 = rangetable.Assigned("6.1.0")

// Use the same token keys for title matching and deduplicating FTS phrases.
// unicode61 defaults to letters, numbers and private-use characters. Code
// points unassigned in Unicode 6.1 are token characters too, and never fold.
func searchTitleTokens(text string) []string {
	if len(text) == 0 {
		return nil
	}
	var b strings.Builder
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			switch {
			case 'A' <= r && r <= 'Z':
				b.WriteByte(byte(r + ('a' - 'A')))
			case 'a' <= r && r <= 'z', '0' <= r && r <= '9':
				b.WriteByte(byte(r))
			default:
				b.WriteByte(' ')
			}
		case r == 0xfffe || r == 0xffff: // SQLite decodes these as U+FFFD (a separator)
			b.WriteByte(' ')
		case !unicode.Is(assigned61, r):
			b.WriteRune(r)
		case unicode61Diacritic(r): // ignored within tokens, skipped between tokens
		case unicode61TokenRune(r):
			b.WriteRune(unicode61Fold(r))
		default:
			b.WriteByte(' ')
		}
	}
	return strings.FieldsFunc(b.String(), func(r rune) bool { return r == ' ' })
}

// These are the combining marks removed by unicode61, not all Unicode marks:
// for example a Devanagari vowel sign is a separator, not a Latin accent.
func unicode61Diacritic(r rune) bool {
	return r >= 0x300 && r <= 0x331 && (uint64(0x000361F808029FDF)>>(r-0x300))&1 != 0
}

// General-category changes since 6.1 that cross the token/separator boundary.
func unicode61TokenRune(r rune) bool {
	switch {
	case r == 0x1885 || r == 0x1886:
		return true // Mongolian letters in 6.1
	case r >= 0x19b0 && r <= 0x19c0, r == 0x19c8, r == 0x19c9, r == 0x1cf2, r == 0x1cf3:
		return false // Tai Lue and Vedic marks in 6.1
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Co, r)
}

func unicode61Fold(r rune) rune {
	// The canonical member of these fold cycles is not the lowest rune; the
	// final three folds were introduced after Unicode 6.1.
	switch r {
	case 'µ', 'Μ', 'μ':
		return 'μ'
	case 'Ι', 'ι', 'ι':
		return 'ι'
	case 'ΐ', 'ΰ', 'ﬆ':
		return r
	}

	// Simple (one rune) case folding, including final sigma and long s; full
	// Unicode folding would incorrectly equate words such as Maße and Masse.
	smallest := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < smallest && unicode.Is(assigned61, f) {
			smallest = f
		}
	}
	fold := unicode.ToLower(smallest)
	if !unicode.Is(assigned61, fold) {
		return r
	}
	if !unicode.Is(unicode.Latin, fold) {
		return fold
	}
	// remove_diacritics=1 removes a single accent on an ASCII Latin base,
	// but deliberately retains precomposed letters with multiple accents.
	d := norm.NFD.PropertiesString(string(fold)).Decomposition()
	if len(d) > 1 && d[0] >= 'a' && d[0] <= 'z' {
		mark, n := utf8.DecodeRune(d[1:])
		if n+1 == len(d) && unicode61Diacritic(mark) {
			return rune(d[0])
		}
	}
	return fold
}
