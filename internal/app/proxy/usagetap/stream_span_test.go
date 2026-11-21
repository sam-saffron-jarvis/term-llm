package usagetap

import (
	"bytes"
	"fmt"
	"testing"
)

// Exact limits apply to the whole event, not the size of the current read.
// SSE counts field prefixes, ignored fields and one byte per line ending;
// NDJSON does not charge its record delimiter.
func TestStreamSpanLimit(t *testing.T) {
	for _, format := range []Format{SSE, NDJSON} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/limit+%d", format, extra), func(t *testing.T) {
				data := []byte(chatJSON)
				padding := MaxEventBytes - len(data) + extra
				if format == SSE {
					data = []byte(":\r\ndata: " + chatJSON)
					padding -= len(":\ndata: ") + 1
				}
				data = append(data, bytes.Repeat([]byte{' '}, padding)...)
				p := New(ChatCompletions, format)
				// Feed the large span separately from the CR/LF split and the empty read.
				p.Feed(data[:7])
				p.Feed(data[7:])
				p.Feed([]byte{'\r'})
				p.Feed(nil)
				p.Feed([]byte{'\n'})
				if format == SSE {
					p.Feed([]byte{'\r'})
				}
				want, total := Reported, int64(19)
				if extra == 1 {
					want, total = Unknown, UnknownTokens
				}
				if got := p.Finish(true); got.Source != want || got.Total != total {
					t.Fatalf("usage = %+v", got)
				}
			})
		}
	}
}

// A provider usually emits many unmetered deltas and a single usage event.
// One operation observes a whole stream; this does not measure HTTP latency.
func BenchmarkResponsesDeltaStream(b *testing.B) {
	delta := []byte(`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}` + "\n\n")
	data := append(bytes.Repeat(delta, 1000), []byte("data: "+responseJSON+"\n\n")...)
	for _, n := range []int{len(delta), 4096} {
		b.Run(fmt.Sprintf("read=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				p := New(Responses, SSE)
				for i := 0; i < len(data); i += n {
					p.Feed(data[i:min(i+n, len(data))])
				}
				if u := p.Finish(true); u.Source != Reported || u.Total != 19 || u.Turns != 1 {
					b.Fatal(u)
				}
			}
		})
	}
}
