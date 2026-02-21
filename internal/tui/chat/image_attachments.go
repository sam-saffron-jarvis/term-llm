package chat

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/samsaffron/term-llm/internal/clipboard"
	"github.com/samsaffron/term-llm/internal/llm"
)

const maxPastedImageSize = 20 * 1024 * 1024

var readClipboardImage = clipboard.ReadImage

// ImageAttachment represents an image attached to the next outgoing user message.
type ImageAttachment struct {
	MediaType string
	Data      []byte
}

func (m *Model) maybeAttachImageFromPaste(msg tea.KeyMsg) bool {
	if !isImagePasteAttempt(msg) {
		return false
	}

	imgData, err := readClipboardImage()
	if err != nil || len(imgData) == 0 {
		return false
	}

	if len(imgData) > maxPastedImageSize {
		return true
	}

	mediaType := detectImageMediaType(imgData)
	if !strings.HasPrefix(mediaType, "image/") {
		return false
	}

	m.images = append(m.images, ImageAttachment{
		MediaType: mediaType,
		Data:      imgData,
	})
	m.selectedImage = -1
	return true
}

func isImagePasteAttempt(msg tea.KeyMsg) bool {
	if msg.Paste || msg.Type == tea.KeyCtrlV {
		return true
	}
	switch strings.ToLower(msg.String()) {
	case "ctrl+shift+v", "shift+insert", "super+v", "cmd+v":
		return true
	default:
		return false
	}
}

func (m *Model) handleImageAttachmentKeys(msg tea.KeyMsg) bool {
	if len(m.images) == 0 || m.textarea.Value() != "" {
		return false
	}

	switch msg.Type {
	case tea.KeyUp:
		if m.selectedImage < 0 {
			m.selectedImage = 0
		} else if m.selectedImage > 0 {
			m.selectedImage--
		}
		return true
	case tea.KeyDown:
		if m.selectedImage >= 0 {
			if m.selectedImage < len(m.images)-1 {
				m.selectedImage++
			} else {
				m.selectedImage = -1
			}
			return true
		}
	case tea.KeyLeft:
		if m.selectedImage > 0 {
			m.selectedImage--
			return true
		}
	case tea.KeyRight:
		if m.selectedImage >= 0 && m.selectedImage < len(m.images)-1 {
			m.selectedImage++
			return true
		}
	case tea.KeyDelete, tea.KeyBackspace:
		if m.selectedImage >= 0 {
			idx := m.selectedImage
			m.images = append(m.images[:idx], m.images[idx+1:]...)
			if len(m.images) == 0 {
				m.selectedImage = -1
			} else if idx >= len(m.images) {
				m.selectedImage = len(m.images) - 1
			}
			return true
		}
	}

	return false
}

func (m *Model) imagePartList() []llm.Part {
	if len(m.images) == 0 {
		return nil
	}
	parts := make([]llm.Part, 0, len(m.images))
	for _, img := range m.images {
		parts = append(parts, llm.Part{
			Type:      llm.PartImage,
			ImageData: &llm.ToolImageData{MediaType: img.MediaType, Base64: base64.StdEncoding.EncodeToString(img.Data)},
		})
	}
	return parts
}

func (m *Model) imageAttachmentLabels() []string {
	labels := make([]string, 0, len(m.images))
	for i := range m.images {
		labels = append(labels, fmt.Sprintf("image %d", i+1))
	}
	return labels
}

func detectImageMediaType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	mediaType := http.DetectContentType(data)
	if mediaType == "application/octet-stream" {
		return "image/png"
	}
	return mediaType
}
