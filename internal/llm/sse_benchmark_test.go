package llm

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func BenchmarkReadSSEEventChatCompletions(b *testing.B) {
	const chunk = `data: {"choices":[{"delta":{"content":"hello world"}}]}` + "\n\n"
	payload := strings.Repeat(chunk, 128) + "data: [DONE]\n\n"

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reader := bufio.NewReader(strings.NewReader(payload))
		for {
			_, data, eof, err := readSSEEvent(reader)
			if err != nil {
				b.Fatalf("readSSEEvent: %v", err)
			}
			if eof && data == "" {
				break
			}
			if data == "" {
				if eof {
					break
				}
				continue
			}
			if data == "[DONE]" {
				break
			}
			if eof {
				break
			}
		}
	}
}

func BenchmarkReadSSEEventBytesChatCompletions(b *testing.B) {
	const chunk = `data: {"choices":[{"delta":{"content":"hello world"}}]}` + "\n\n"
	payload := strings.Repeat(chunk, 128) + "data: [DONE]\n\n"

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		reader := newSSEEventReader(bufio.NewReader(strings.NewReader(payload)))
		for {
			_, data, eof, err := reader.readEventBytes()
			if err != nil {
				b.Fatalf("readSSEEventBytes: %v", err)
			}
			if eof && len(data) == 0 {
				break
			}
			if len(data) == 0 {
				if eof {
					break
				}
				continue
			}
			if bytes.Equal(data, sseDoneData) {
				break
			}
			if eof {
				break
			}
		}
	}
}
