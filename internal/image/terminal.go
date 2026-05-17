package image

import (
	"fmt"
	"io"

	"github.com/samsaffron/term-llm/internal/termimage"
)

// TerminalImageCapability represents the terminal's image rendering capability.
type TerminalImageCapability int

const (
	CapNone  TerminalImageCapability = iota // No image support
	CapKitty                                // Kitty graphics protocol
	CapITerm                                // iTerm2 inline images
	CapSixel                                // Sixel graphics
)

// String returns the capability name.
func (c TerminalImageCapability) String() string {
	switch c {
	case CapKitty:
		return "kitty"
	case CapITerm:
		return "iterm"
	case CapSixel:
		return "sixel"
	default:
		return "none"
	}
}

// DetectCapability detects the terminal's image rendering capability.
//
// This package intentionally delegates terminal protocol detection/rendering to
// internal/termimage so command paths and TUI image artifacts share one Kitty
// implementation. ANSI/text fallback is reported as CapNone here because this
// legacy API is used only for direct terminal image display.
func DetectCapability() TerminalImageCapability {
	strategy := termimage.Select(termimage.Request{
		Mode:               termimage.ModeOneShot,
		Protocol:           termimage.ProtocolAuto,
		AllowEscapeUploads: true,
	}, termimage.DefaultEnvironment())
	return capabilityFromProtocol(strategy.Protocol)
}

func capabilityFromProtocol(protocol termimage.Protocol) TerminalImageCapability {
	switch protocol {
	case termimage.ProtocolKitty:
		return CapKitty
	case termimage.ProtocolITerm:
		return CapITerm
	case termimage.ProtocolSixel:
		return CapSixel
	default:
		return CapNone
	}
}

// RenderImageResult contains the result of rendering an image.
type RenderImageResult struct {
	// Upload contains terminal control bytes that must go directly to terminal.
	Upload string
	// Placeholder contains the displayable portion.
	Placeholder string
	// Full is Upload + Placeholder for one-shot display.
	Full string
}

// RenderImageToString renders an image and returns terminal image bytes for display.
func RenderImageToString(path string) (RenderImageResult, error) {
	result, err := renderViaTermimage(path)
	if err != nil {
		return RenderImageResult{}, err
	}
	if capabilityFromProtocol(result.Protocol) == CapNone {
		return RenderImageResult{}, nil
	}
	return RenderImageResult{
		Upload:      result.Upload,
		Placeholder: result.Display,
		Full:        result.Full,
	}, nil
}

// RenderImageToWriter renders an image to a writer using the detected capability.
func RenderImageToWriter(w io.Writer, path string) error {
	result, err := renderViaTermimage(path)
	if err != nil {
		return err
	}
	if capabilityFromProtocol(result.Protocol) == CapNone || result.Full == "" {
		return nil
	}
	_, err = io.WriteString(w, result.Full)
	return err
}

func renderViaTermimage(path string) (termimage.Result, error) {
	result, err := termimage.Render(termimage.Request{
		Path:               path,
		Mode:               termimage.ModeOneShot,
		Protocol:           termimage.ProtocolAuto,
		MaxCols:            termimage.DefaultMaxCols,
		MaxRows:            termimage.DefaultMaxRows,
		AllowEscapeUploads: true,
	})
	if err != nil {
		return termimage.Result{}, fmt.Errorf("render terminal image: %w", err)
	}
	return result, nil
}
