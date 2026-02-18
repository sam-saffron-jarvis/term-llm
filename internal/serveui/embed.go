package serveui

import _ "embed"

//go:embed static/index.html
var indexHTML []byte

// IndexHTML returns the embedded UI page.
func IndexHTML() []byte {
	out := make([]byte, len(indexHTML))
	copy(out, indexHTML)
	return out
}
