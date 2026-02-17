package llm

import (
	"encoding/base64"
	"strings"
)

func toolResultContentParts(result *ToolResult) []ToolContentPart {
	if result == nil {
		return nil
	}
	if len(result.ContentParts) > 0 {
		return result.ContentParts
	}
	if result.Content == "" {
		return nil
	}
	return []ToolContentPart{{
		Type: ToolContentPartText,
		Text: result.Content,
	}}
}

func toolResultTextContent(result *ToolResult) string {
	if result == nil {
		return ""
	}
	if len(result.ContentParts) == 0 {
		return result.Content
	}

	var b strings.Builder
	for _, part := range result.ContentParts {
		if part.Type == ToolContentPartText && part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return result.Content
	}
	return b.String()
}

func toolResultImageData(part ToolContentPart) (mediaType, base64Data string, ok bool) {
	if part.Type != ToolContentPartImageData || part.ImageData == nil {
		return "", "", false
	}

	mediaType = strings.TrimSpace(part.ImageData.MediaType)
	base64Data = strings.TrimSpace(part.ImageData.Base64)
	if !isSupportedToolResultImageMediaType(mediaType) || base64Data == "" {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(base64Data); err != nil {
		return "", "", false
	}
	return mediaType, base64Data, true
}

func toolResultHasImageData(result *ToolResult) bool {
	for _, part := range toolResultContentParts(result) {
		if _, _, ok := toolResultImageData(part); ok {
			return true
		}
	}
	return false
}

func isSupportedToolResultImageMediaType(mimeType string) bool {
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
