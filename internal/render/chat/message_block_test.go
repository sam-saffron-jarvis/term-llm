package chat

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/samsaffron/term-llm/internal/llm"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/ui"
)

func TestMessageBlockRenderer_UserMessageBackground_UsesTruecolorTheme(t *testing.T) {
	renderer := NewMessageBlockRenderer(80, nil, false)
	msg := &session.Message{
		ID:          1,
		Role:        llm.RoleUser,
		TextContent: "contrast check",
	}

	rendered := renderer.renderUserMessage(msg)
	if !strings.Contains(rendered, "\x1b[48;2;60;56;54m") {
		t.Fatalf("expected truecolor user message background #3c3836, got %q", rendered)
	}
}

func TestMessageBlockRenderer_UserMessageRowsFillRendererWidth(t *testing.T) {
	const width = 24
	renderer := NewMessageBlockRenderer(width, nil, false)
	msg := &session.Message{
		ID:   1,
		Role: llm.RoleUser,
		Parts: []llm.Part{
			{Type: llm.PartText, Text: "alpha beta gamma delta epsilon\n\nwide 😀 text"},
			{Type: llm.PartImage, ImageData: &llm.ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="}},
		},
	}

	rendered := renderer.renderUserMessage(msg)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 5 {
		t.Fatalf("expected wrapped content, blank content line, attachment metadata, and trailing blank line; got %d lines in %q", len(lines), rendered)
	}

	messageRows := lines[:len(lines)-2] // Keep the final inter-message blank line unstyled/unpadded.
	if len(messageRows) < 4 {
		t.Fatalf("expected at least 4 user-message rows, got %d in %q", len(messageRows), rendered)
	}

	foundBlankContentRow := false
	foundAttachmentMetaRow := false
	for i, line := range messageRows {
		plain := ui.StripANSI(line)
		if got := xansi.StringWidth(plain); got != width {
			t.Fatalf("message row %d width = %d, want %d; plain=%q rendered=%q", i, got, width, plain, line)
		}
		if strings.TrimSpace(plain) == "" {
			foundBlankContentRow = true
		}
		if strings.Contains(plain, "[with: image 1]") {
			foundAttachmentMetaRow = true
		}
	}
	if !foundBlankContentRow {
		t.Fatalf("expected intentional blank content row to be padded to width in %q", rendered)
	}
	if !foundAttachmentMetaRow {
		t.Fatalf("expected attachment metadata row in %q", rendered)
	}

	if lines[len(lines)-2] != "" || lines[len(lines)-1] != "" {
		t.Fatalf("expected final inter-message blank line to remain unstyled, got trailing split lines %q", lines[len(lines)-2:])
	}
}

func TestMessageBlockRenderer_UserMessageWithImageParts_ShowsAttachmentMeta(t *testing.T) {
	renderer := NewMessageBlockRenderer(80, nil, false)
	msg := &session.Message{
		ID:          1,
		Role:        llm.RoleUser,
		TextContent: "describe this",
		Parts: []llm.Part{
			{Type: llm.PartImage, ImageData: &llm.ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="}},
			{Type: llm.PartText, Text: "describe this"},
		},
	}

	rendered := renderer.renderUserMessage(msg)
	if !strings.Contains(rendered, "describe this") {
		t.Fatalf("expected user text in rendered message, got %q", rendered)
	}
	if !strings.Contains(rendered, "[with: image 1]") {
		t.Fatalf("expected image attachment meta, got %q", rendered)
	}
}

func TestMessageBlockRenderer_ImageOnlyUserMessage_ShowsPlaceholderAndAttachmentMeta(t *testing.T) {
	renderer := NewMessageBlockRenderer(80, nil, false)
	msg := &session.Message{
		ID:          1,
		Role:        llm.RoleUser,
		TextContent: "[image 1]",
		Parts: []llm.Part{
			{Type: llm.PartImage, ImageData: &llm.ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="}},
		},
	}

	rendered := renderer.renderUserMessage(msg)
	if !strings.Contains(rendered, "[image]") {
		t.Fatalf("expected image-only placeholder, got %q", rendered)
	}
	if !strings.Contains(rendered, "[with: image 1]") {
		t.Fatalf("expected image attachment meta, got %q", rendered)
	}
}

func TestMessageBlockRenderer_MultipleImages_ShowsCountedAttachmentMeta(t *testing.T) {
	renderer := NewMessageBlockRenderer(80, nil, false)
	msg := &session.Message{
		ID:          1,
		Role:        llm.RoleUser,
		TextContent: "compare these",
		Parts: []llm.Part{
			{Type: llm.PartImage, ImageData: &llm.ToolImageData{MediaType: "image/png", Base64: "aGVsbG8="}},
			{Type: llm.PartImage, ImageData: &llm.ToolImageData{MediaType: "image/jpeg", Base64: "d29ybGQ="}},
			{Type: llm.PartText, Text: "compare these"},
		},
	}

	rendered := renderer.renderUserMessage(msg)
	if !strings.Contains(rendered, "[with: 2 images]") {
		t.Fatalf("expected counted image attachment meta, got %q", rendered)
	}
}
