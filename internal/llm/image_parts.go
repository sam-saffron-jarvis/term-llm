package llm

import (
	"encoding/base64"
	"os"
	"strings"
)

func inlineImagePath(part Part) string {
	if path := strings.TrimSpace(part.InlineImagePath); path != "" {
		return path
	}
	return strings.TrimSpace(part.ImagePath)
}

func requestImageData(part Part) (mediaType, base64Data string, ok bool) {
	if part.ImageData != nil {
		mediaType = strings.TrimSpace(part.ImageData.MediaType)
		base64Data = strings.TrimSpace(part.ImageData.Base64)
		if mediaType != "" && base64Data != "" {
			return mediaType, base64Data, true
		}
	}

	path := inlineImagePath(part)
	if path == "" {
		return "", "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	if mediaType == "" {
		mediaType = mediaTypeFromPath(path)
	}
	if mediaType == "" {
		return "", "", false
	}
	return mediaType, base64.StdEncoding.EncodeToString(data), true
}

func requestImageBytes(part Part) (mediaType string, data []byte, ok bool) {
	if part.ImageData != nil {
		mediaType = strings.TrimSpace(part.ImageData.MediaType)
		base64Data := strings.TrimSpace(part.ImageData.Base64)
		if mediaType != "" && base64Data != "" {
			decoded, err := base64.StdEncoding.DecodeString(base64Data)
			if err == nil {
				return mediaType, decoded, true
			}
		}
	}

	path := inlineImagePath(part)
	if path == "" {
		return "", nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, false
	}
	if mediaType == "" {
		mediaType = mediaTypeFromPath(path)
	}
	if mediaType == "" {
		return "", nil, false
	}
	return mediaType, data, true
}
