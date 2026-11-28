package requestmeta

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func BenchmarkRequestObservation(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"small_1KiB", 1 << 10}, {"large_512KiB", 512 << 10}, {"overbudget_2MiB", 2 << 20}} {
		body := `{"model":"gpt-5.6","reasoning":{"effort":"high"},"input":"` + strings.Repeat("x", size.n) + `"}`
		for _, mode := range []string{"baseline", "known_length", "unknown_length"} {
			b.Run(size.name+"/"+mode, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				buf := make([]byte, 32<<10)
				for b.Loop() {
					var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
					var observer *Observer
					if mode != "baseline" {
						length := int64(len(body))
						if mode == "unknown_length" {
							length = -1
						}
						observer = New(reader, context.Background(), "POST", "/v1/responses", "/responses", "openai", "https://api.openai.com/v1", http.Header{"Content-Type": {"application/json"}}, length)
						reader = observer
					}
					for {
						_, err := reader.Read(buf)
						if err != nil {
							break
						}
					}
					_ = reader.Close()
					if observer != nil {
						_ = observer.Finalize()
					}
				}
			})
		}
	}
}

func BenchmarkConcurrentOversizeRequests(b *testing.B) {
	body := strings.Repeat("x", 2<<20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, 32<<10)
		for pb.Next() {
			observer := newObserver(io.NopCloser(strings.NewReader(body)), int64(len(body)), context.Background())
			for {
				_, err := observer.Read(buf)
				if err != nil {
					break
				}
			}
			_ = observer.Finalize()
			_ = observer.Close()
		}
	})
}
