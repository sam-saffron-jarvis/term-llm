package streaming

// StreamRendererOption configures a StreamRenderer.
type StreamRendererOption func(*StreamRenderer)

// WithPartialRendering enables partial block rendering with re-rendering.
// When enabled, safe text within incomplete blocks will be shown immediately,
// and the output will be re-rendered when syntax completes.
func WithPartialRendering() StreamRendererOption {
	return func(sr *StreamRenderer) {
		sr.partialEnabled = true
	}
}

// WithTerminalWidth sets the terminal width for accurate line counting
// during partial rendering. This is used to calculate how many lines
// the rendered output occupies for cursor repositioning.
func WithTerminalWidth(width int) StreamRendererOption {
	return func(sr *StreamRenderer) {
		sr.termWidth = width
	}
}
