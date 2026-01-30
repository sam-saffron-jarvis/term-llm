package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
)

// generateMessages creates a slice of test messages for benchmarking.
func generateMessages(count int) []session.Message {
	messages := make([]session.Message, count)
	for i := 0; i < count; i++ {
		if i%2 == 0 {
			// User message
			messages[i] = session.Message{
				ID:          int64(i + 1),
				Role:        llm.RoleUser,
				TextContent: fmt.Sprintf("This is user message %d with some content that might span multiple lines to simulate realistic message lengths.", i),
				Parts: []llm.Part{
					{Type: llm.PartText, Text: fmt.Sprintf("This is user message %d with some content that might span multiple lines to simulate realistic message lengths.", i)},
				},
				CreatedAt: time.Now(),
				Sequence:  i,
			}
		} else {
			// Assistant message
			text := fmt.Sprintf("This is assistant message %d. Here is a detailed response:\n\n1. First point\n2. Second point\n3. Third point\n\nHere's some code:\n```go\nfunc example() {\n    fmt.Println(\"Hello\")\n}\n```\n\nAnd a final thought.", i)
			messages[i] = session.Message{
				ID:          int64(i + 1),
				Role:        llm.RoleAssistant,
				TextContent: text,
				Parts: []llm.Part{
					{Type: llm.PartText, Text: text},
				},
				CreatedAt: time.Now(),
				Sequence:  i,
			}
		}
	}
	return messages
}

// simpleMarkdownRenderer is a no-op renderer for benchmarking.
func simpleMarkdownRenderer(content string, width int) string {
	// Simple word wrap simulation
	if len(content) < width {
		return content
	}
	// Just return content as-is for benchmarking (real glamour would be slower)
	return content
}

func TestRenderer_Render(t *testing.T) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(10)
	state := RenderState{
		Messages: messages,
		Viewport: ViewportState{
			Height:       24,
			ScrollOffset: 0,
			AtBottom:     true,
		},
		Mode:   RenderModeAltScreen,
		Width:  80,
		Height: 24,
	}

	output := renderer.Render(state)

	// Should contain some content
	if output == "" {
		t.Error("Render() returned empty string for non-empty messages")
	}

	// Should contain user prompts
	if !strings.Contains(output, "❯") {
		t.Error("Render() output should contain user prompt markers")
	}
}

func TestRenderer_CacheHit(t *testing.T) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(5)
	state := RenderState{
		Messages: messages,
		Viewport: ViewportState{Height: 24},
		Mode:     RenderModeAltScreen,
		Width:    80,
		Height:   24,
	}

	// First render - cache miss
	_ = renderer.Render(state)
	cacheSize1 := renderer.blockCache.Size()

	// Second render - should use cache
	_ = renderer.Render(state)
	cacheSize2 := renderer.blockCache.Size()

	// Cache size should be the same (no new entries)
	if cacheSize2 != cacheSize1 {
		t.Errorf("Cache size changed on second render: %d -> %d", cacheSize1, cacheSize2)
	}

	if cacheSize1 == 0 {
		t.Error("Cache should have entries after render")
	}
}

func TestRenderer_CacheInvalidateOnResize(t *testing.T) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(5)
	state := RenderState{
		Messages: messages,
		Viewport: ViewportState{Height: 24},
		Mode:     RenderModeAltScreen,
		Width:    80,
		Height:   24,
	}

	// First render
	_ = renderer.Render(state)
	cacheSize1 := renderer.blockCache.Size()

	// Resize
	renderer.SetSize(100, 30)

	// Cache should be invalidated
	cacheSize2 := renderer.blockCache.Size()
	if cacheSize2 != 0 {
		t.Errorf("Cache should be empty after resize, got %d", cacheSize2)
	}

	// Render again with new size
	state.Width = 100
	state.Height = 30
	_ = renderer.Render(state)
	cacheSize3 := renderer.blockCache.Size()

	if cacheSize3 != cacheSize1 {
		t.Logf("Cache rebuilt after resize: %d entries", cacheSize3)
	}
}

func TestVirtualViewport_GetVisibleRange(t *testing.T) {
	vp := NewVirtualViewport(80, 24)

	tests := []struct {
		name         string
		msgCount     int
		scrollOffset int
		wantStart    int // Approximate - just check it's reasonable
		wantEnd      int
	}{
		{"empty", 0, 0, 0, 0},
		{"small list no scroll", 5, 0, 0, 5},
		{"small list scroll up 1", 5, 1, 0, 4},
		{"large list no scroll", 100, 0, -1, 100}, // -1 means "check > 0"
		{"large list scroll up", 100, 10, -1, 90},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := generateMessages(tt.msgCount)
			start, end := vp.GetVisibleRange(messages, tt.scrollOffset)

			if tt.wantStart >= 0 && start != tt.wantStart {
				t.Errorf("start = %d, want %d", start, tt.wantStart)
			}
			if tt.wantStart == -1 && start < 0 {
				t.Errorf("start = %d, want >= 0", start)
			}
			if end != tt.wantEnd {
				t.Errorf("end = %d, want %d", end, tt.wantEnd)
			}

			// Sanity checks
			if start < 0 {
				t.Errorf("start = %d, should be >= 0", start)
			}
			if end > tt.msgCount {
				t.Errorf("end = %d, should be <= %d", end, tt.msgCount)
			}
			if start > end {
				t.Errorf("start (%d) > end (%d)", start, end)
			}
		})
	}
}

// BenchmarkRender500Messages benchmarks rendering 500 messages.
// Target: <16ms for 60fps rendering.
func BenchmarkRender500Messages(b *testing.B) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(500)
	state := RenderState{
		Messages: messages,
		Viewport: ViewportState{
			Height:       24,
			ScrollOffset: 0,
			AtBottom:     true,
		},
		Mode:   RenderModeAltScreen,
		Width:  80,
		Height: 24,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderer.Render(state)
	}
}

// BenchmarkRender500MessagesCached benchmarks with warm cache.
func BenchmarkRender500MessagesCached(b *testing.B) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(500)
	state := RenderState{
		Messages: messages,
		Viewport: ViewportState{
			Height:       24,
			ScrollOffset: 0,
			AtBottom:     true,
		},
		Mode:   RenderModeAltScreen,
		Width:  80,
		Height: 24,
	}

	// Warm up cache
	renderer.Render(state)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderer.Render(state)
	}
}

// BenchmarkRender500MessagesScrolling benchmarks with scroll position changes.
func BenchmarkRender500MessagesScrolling(b *testing.B) {
	renderer := NewRenderer(80, 24)
	renderer.SetMarkdownRenderer(simpleMarkdownRenderer)

	messages := generateMessages(500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate scrolling by changing offset each iteration
		state := RenderState{
			Messages: messages,
			Viewport: ViewportState{
				Height:       24,
				ScrollOffset: i % 100,
				AtBottom:     i%100 == 0,
			},
			Mode:   RenderModeAltScreen,
			Width:  80,
			Height: 24,
		}
		renderer.Render(state)
	}
}
