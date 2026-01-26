package ui

import "strings"

// FindSafeBoundary finds the last byte position where markdown context is complete.
// Returns -1 if no safe boundary exists.
//
// A safe boundary is a position after which we can split the text without
// breaking markdown rendering. Safe positions are:
// - After complete paragraphs (\n\n)
// - After closed code blocks (balanced ``` markers)
// - When not inside incomplete inline markers (**, `, etc.)
func FindSafeBoundary(text string) int {
	if len(text) < 20 {
		return -1 // Too short to bother caching
	}

	// Find all paragraph boundaries and check them from last to first
	// We want the latest safe boundary
	pos := len(text)
	for {
		paraEnd := strings.LastIndex(text[:pos], "\n\n")
		if paraEnd == -1 {
			return -1 // No paragraph boundary found
		}

		safePos := paraEnd + 2

		// Skip if inside code block
		if isInCodeBlock(text, safePos) {
			pos = paraEnd
			continue
		}

		// Check if inline markers are balanced up to this point
		if areInlineMarkersBalanced(text[:safePos]) {
			return safePos
		}

		// Try earlier boundary
		pos = paraEnd
	}
}

// isInCodeBlock returns true if position pos is inside an unclosed code block.
// Code blocks are delimited by ``` at the start of a line.
func isInCodeBlock(text string, pos int) bool {
	if pos > len(text) {
		pos = len(text)
	}

	prefix := text[:pos]
	count := countCodeFences(prefix)

	// Odd number of fences means we're inside a code block
	return count%2 == 1
}

// countCodeFences counts ``` markers that appear at the start of a line.
func countCodeFences(text string) int {
	count := 0
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			count++
		}
	}
	return count
}

// areInlineMarkersBalanced checks if common inline markdown markers are balanced.
// This checks for paired **, *, `, ~~ markers outside of code spans.
func areInlineMarkersBalanced(text string) bool {
	// Track if we're inside various inline elements
	inCodeSpan := false
	inBold := false
	inItalicAsterisk := false
	inItalicUnderscore := false
	inStrikethrough := false

	i := 0
	for i < len(text) {
		// Handle code spans first (they escape other markers)
		if !inCodeSpan && i < len(text) && text[i] == '`' {
			// Count consecutive backticks
			start := i
			for i < len(text) && text[i] == '`' {
				i++
			}
			backtickCount := i - start
			// Look for matching closing backticks
			closing := strings.Repeat("`", backtickCount)
			closeIdx := strings.Index(text[i:], closing)
			if closeIdx == -1 {
				return false // Unclosed code span
			}
			i += closeIdx + backtickCount
			continue
		}

		// Check for bold/italic with asterisks
		if text[i] == '*' {
			if i+1 < len(text) && text[i+1] == '*' {
				// ** - bold
				inBold = !inBold
				i += 2
				continue
			}
			// Single * - italic
			inItalicAsterisk = !inItalicAsterisk
			i++
			continue
		}

		// Check for italic with underscore (only at word boundaries)
		if text[i] == '_' {
			// Simplified: just toggle state
			inItalicUnderscore = !inItalicUnderscore
			i++
			continue
		}

		// Check for strikethrough
		if text[i] == '~' && i+1 < len(text) && text[i+1] == '~' {
			inStrikethrough = !inStrikethrough
			i += 2
			continue
		}

		i++
	}

	// All markers should be closed
	return !inCodeSpan && !inBold && !inItalicAsterisk && !inItalicUnderscore && !inStrikethrough
}
