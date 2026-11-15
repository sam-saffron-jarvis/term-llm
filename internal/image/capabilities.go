package image

import (
	"context"
	"fmt"
	"math"

	"github.com/term-llm/term-llm/internal/mediautil"
)

// NativeSize explicitly omits Venice resolution without consulting a catalog.
const NativeSize = "native"

// VeniceControls contains generation options specific to Venice. Nil fields are omitted.
type VeniceControls struct {
	Width, Height, Steps *int
	CFGScale             *float64
	NegativePrompt       *string
	SafeMode             *bool
}

// VeniceControlsProvider identifies providers that support Venice generation controls.
type VeniceControlsProvider interface{ SupportsVeniceControls() bool }

// ImageProcessingProvider is an optional capability for local image processing.
type ImageProcessingProvider interface {
	Upscale(context.Context, UpscaleRequest) (*ImageResult, error)
	RemoveBackground(context.Context, BackgroundRemovalRequest) (*ImageResult, error)
}

type UpscaleRequest struct {
	Image                          InputImage
	Scale                          *int
	Enhance                        *bool
	EnhancePrompt                  *string
	EnhanceCreativity, Replication *float64
	Debug, DebugRaw                bool
}

type BackgroundRemovalRequest struct {
	Image           InputImage
	Debug, DebugRaw bool
}

// ValidateVeniceControls rejects unsupported controls and ambiguous dimensions.
func ValidateVeniceControls(provider ImageProvider, c *VeniceControls, size, aspectRatio string) error {
	if c == nil && size != NativeSize {
		return nil
	}
	capable, ok := provider.(VeniceControlsProvider)
	if !ok || !capable.SupportsVeniceControls() {
		return fmt.Errorf("provider does not support Venice image controls or native sizing")
	}
	if c == nil {
		return nil
	}
	if (c.Width == nil) != (c.Height == nil) {
		return fmt.Errorf("width and height must be specified together")
	}
	if c.Width != nil && (*c.Width <= 0 || *c.Height <= 0) {
		return fmt.Errorf("width and height must be positive")
	}
	if c.Width != nil && (size != "" || aspectRatio != "") {
		return fmt.Errorf("pixel dimensions cannot be combined with size or aspect ratio")
	}
	if c.Steps != nil && *c.Steps <= 0 {
		return fmt.Errorf("steps must be positive")
	}
	if c.CFGScale != nil && (math.IsNaN(*c.CFGScale) || math.IsInf(*c.CFGScale, 0)) {
		return fmt.Errorf("cfg scale must be finite")
	}
	return nil
}

// ValidateEditInputs validates remote references and capability support.
func ValidateEditInputs(provider ImageProvider, inputs []InputImage) error {
	for _, img := range inputs {
		if img.URL == "" {
			if mediautil.IsRemoteReference(img.Path) {
				return fmt.Errorf("remote image must use URL field")
			}
			continue
		}
		if err := mediautil.ValidateRemoteURL(img.URL); err != nil {
			return fmt.Errorf("invalid image URL: %w", err)
		}
		if len(img.Data) != 0 || img.Path != "" {
			return fmt.Errorf("image URL cannot be combined with local data or path")
		}
		if _, ok := provider.(*VeniceProvider); !ok {
			return fmt.Errorf("provider does not support remote image URLs")
		}
	}
	return nil
}

// ValidateEditModel rejects an explicit edit-model override on unsupported providers.
func ValidateEditModel(provider ImageProvider, model string) error {
	if model == "" {
		return nil
	}
	if _, ok := provider.(*VeniceProvider); !ok {
		return fmt.Errorf("provider does not support an explicit edit model")
	}
	return nil
}
