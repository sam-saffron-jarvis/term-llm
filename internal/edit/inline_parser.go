// Package edit provides editing functionality for the term-llm.
package edit

import (
	"regexp"
	"strings"
)

// InlineEditType indicates the type of inline edit.
type InlineEditType int

const (
	InlineEditInsert InlineEditType = iota
	InlineEditDelete
)

// InlineEdit represents a parsed inline edit marker.
type InlineEdit struct {
	Type    InlineEditType
	After   string   // For INSERT: anchor text to insert after
	From    string   // For DELETE: start line text
	To      string   // For DELETE: end line text (empty for single line)
	Content []string // For INSERT: lines to insert
}

// InlineEditParser parses inline edit markers from streaming text.
// It buffers text and emits edits as complete markers are detected.
type InlineEditParser struct {
	buffer          strings.Builder
	pendingText     strings.Builder // Text that hasn't been emitted yet
	insideInsert    bool
	insertAfter     string
	insertContent   strings.Builder
	OnEdit          func(edit InlineEdit)
	OnText          func(text string)               // Text outside of markers
	OnPartialInsert func(after string, line string) // Streaming line during INSERT
	emittedLines    int                             // Number of lines already emitted for current INSERT
}

// NewInlineEditParser creates a new inline edit parser.
func NewInlineEditParser() *InlineEditParser {
	return &InlineEditParser{}
}

// Regular expressions for matching markers
var (
	// Match <INSERT after="..."> or <INSERT>
	insertOpenRe = regexp.MustCompile(`(?i)<INSERT(?:\s+after="([^"]*)")?\s*>`)
	// Match </INSERT>
	insertCloseRe = regexp.MustCompile(`(?i)</INSERT>`)
	// Match <DELETE from="..." /> or <DELETE from="..." to="..." />
	deleteRe = regexp.MustCompile(`(?i)<DELETE\s+from="([^"]*)"(?:\s+to="([^"]*)")?\s*/>`)
)

// Feed processes a chunk of text from the stream.
func (p *InlineEditParser) Feed(chunk string) {
	p.buffer.WriteString(chunk)
	p.process()
}

// Flush processes any remaining buffered content.
func (p *InlineEditParser) Flush() {
	// If we're inside an insert that never closed, emit the content as text
	if p.insideInsert {
		if p.OnText != nil {
			// Emit the original opening tag as text
			p.OnText("<INSERT")
			if p.insertAfter != "" {
				p.OnText(` after="` + p.insertAfter + `"`)
			}
			p.OnText(">")
			p.OnText(p.insertContent.String())
		}
		p.insideInsert = false
		p.insertAfter = ""
		p.insertContent.Reset()
	}

	// Emit any remaining buffered text
	remaining := p.buffer.String()
	if remaining != "" && p.OnText != nil {
		p.OnText(remaining)
	}
	p.buffer.Reset()
}

func (p *InlineEditParser) process() {
	for {
		text := p.buffer.String()
		if text == "" {
			break
		}

		if p.insideInsert {
			// Look for closing </INSERT>
			loc := insertCloseRe.FindStringIndex(text)
			if loc != nil {
				// Found closing tag - emit the insert edit
				content := text[:loc[0]]
				p.insertContent.WriteString(content)

				// Emit any remaining partial lines via OnPartialInsert before OnEdit
				p.emitPartialLines()

				if p.OnEdit != nil {
					lines := splitLines(p.insertContent.String())
					p.OnEdit(InlineEdit{
						Type:    InlineEditInsert,
						After:   p.insertAfter,
						Content: lines,
					})
				}

				// Reset state and continue processing after the closing tag
				p.insideInsert = false
				p.insertAfter = ""
				p.insertContent.Reset()
				p.emittedLines = 0
				p.buffer.Reset()
				p.buffer.WriteString(text[loc[1]:])
				continue
			}

			// No closing tag yet - check if we might have a partial tag
			// Look for potential start of </INSERT>
			partialIdx := strings.LastIndex(text, "<")
			if partialIdx >= 0 && partialIdx > len(text)-10 {
				// Might be a partial closing tag, buffer the content before it
				if partialIdx > 0 {
					p.insertContent.WriteString(text[:partialIdx])
				}
				p.buffer.Reset()
				p.buffer.WriteString(text[partialIdx:])
				// Emit any complete lines we've accumulated
				p.emitPartialLines()
				return
			}

			// No partial tag, add all content to insert buffer
			p.insertContent.WriteString(text)
			p.buffer.Reset()
			// Emit any complete lines we've accumulated
			p.emitPartialLines()
			return
		}

		// Not inside insert - look for INSERT or DELETE markers

		// Find positions of potential markers
		insertLoc := insertOpenRe.FindStringSubmatchIndex(text)
		deleteLoc := deleteRe.FindStringSubmatchIndex(text)

		// Find which comes first
		var firstMarkerStart int = -1
		var markerType string

		if insertLoc != nil && (deleteLoc == nil || insertLoc[0] < deleteLoc[0]) {
			firstMarkerStart = insertLoc[0]
			markerType = "insert"
		} else if deleteLoc != nil {
			firstMarkerStart = deleteLoc[0]
			markerType = "delete"
		}

		if firstMarkerStart == -1 {
			// No complete markers found - check for partial markers
			partialIdx := strings.LastIndex(text, "<")
			if partialIdx >= 0 && partialIdx > len(text)-20 {
				// Might be start of a marker - emit text before it, keep the rest
				if partialIdx > 0 && p.OnText != nil {
					p.OnText(text[:partialIdx])
				}
				p.buffer.Reset()
				p.buffer.WriteString(text[partialIdx:])
				return
			}

			// No markers at all - emit all text
			if p.OnText != nil {
				p.OnText(text)
			}
			p.buffer.Reset()
			return
		}

		// Emit text before the marker
		if firstMarkerStart > 0 && p.OnText != nil {
			p.OnText(text[:firstMarkerStart])
		}

		if markerType == "insert" {
			// Extract the 'after' attribute
			p.insertAfter = ""
			if insertLoc[2] >= 0 && insertLoc[3] >= 0 {
				p.insertAfter = text[insertLoc[2]:insertLoc[3]]
			}
			p.insideInsert = true
			p.insertContent.Reset()
			p.emittedLines = 0
			p.buffer.Reset()
			p.buffer.WriteString(text[insertLoc[1]:])
		} else if markerType == "delete" {
			// Parse DELETE marker
			from := ""
			to := ""
			if deleteLoc[2] >= 0 && deleteLoc[3] >= 0 {
				from = text[deleteLoc[2]:deleteLoc[3]]
			}
			if deleteLoc[4] >= 0 && deleteLoc[5] >= 0 {
				to = text[deleteLoc[4]:deleteLoc[5]]
			}

			if p.OnEdit != nil {
				p.OnEdit(InlineEdit{
					Type: InlineEditDelete,
					From: from,
					To:   to,
				})
			}

			p.buffer.Reset()
			p.buffer.WriteString(text[deleteLoc[1]:])
		}
	}
}

// emitPartialLines emits any complete lines that haven't been emitted yet.
// This enables streaming INSERT content line-by-line as it arrives.
func (p *InlineEditParser) emitPartialLines() {
	if p.OnPartialInsert == nil {
		return
	}

	content := p.insertContent.String()
	// Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")

	// Split into lines (keep empty strings for blank lines)
	lines := strings.Split(content, "\n")

	// Emit any new complete lines (all but the last, which may be incomplete)
	// Unless the content ends with \n, in which case all lines are complete
	completeLines := len(lines) - 1
	if strings.HasSuffix(content, "\n") && len(lines) > 0 {
		// Last element after split on trailing \n is empty, so all actual lines are complete
		completeLines = len(lines) - 1
	}

	// Emit lines we haven't emitted yet
	for i := p.emittedLines; i < completeLines; i++ {
		line := lines[i]
		// Skip leading empty line if it's the first line (from leading \n after tag)
		if i == 0 && line == "" {
			p.emittedLines++
			continue
		}
		p.OnPartialInsert(p.insertAfter, line)
		p.emittedLines++
	}
}

// splitLines splits content into lines, handling both \n and \r\n.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	// Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")
	// Trim leading/trailing newlines from the content block
	content = strings.Trim(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
