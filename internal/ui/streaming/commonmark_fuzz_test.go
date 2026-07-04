package streaming

import "testing"

func FuzzCommonMarkFullSpecChunking(f *testing.F) {
	// Keep normal `go test` fast: broader CommonMark coverage lives in deterministic
	// spec/parity tests, while fuzzing starts from a compact representative seed.
	f.Add("# Heading\n\nParagraph with **bold** and `code`.\n\n- one\n- two\n", int64(1), byte(0))

	f.Fuzz(func(t *testing.T, input string, seed int64, mode byte) {
		if len(input) > 4096 {
			t.Skip()
		}
		content := []byte(input)
		want := renderDirectBytes(t, content)

		var chunks [][]byte
		switch mode % 3 {
		case 0:
			chunks = splitIntoNPieces(content, 16)
		case 1:
			chunks = randomChunks(content, seed, 32)
		default:
			chunks = adversarialMarkdownChunks(content)
		}
		got := renderStreamedBytes(t, chunks)
		assertRenderedEqual(t, want, got, chunks)
	})
}
