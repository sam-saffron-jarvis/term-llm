// Package image implements image operation validation and request preparation.
package image

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	media "github.com/term-llm/term-llm/internal/image"
	"github.com/term-llm/term-llm/internal/mediautil"
)

// Options captures explicit CLI presence independently of default values.
type Options struct {
	TimeoutExplicit                        bool
	Operation, EditModel                   string
	Controls                               media.VeniceControls
	Upscale                                media.UpscaleRequest
	Timeout                                time.Duration
	Inputs                                 []string
	Size, AspectRatio, Quality, Background string
	Changed                                []string
	Debug, DebugRaw                        bool
}

// Validate runs before prompt/stdin or local file reads.
func (o Options) Validate(hasPrompt bool) error {
	if o.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}
	if o.Operation != "" && o.Operation != "upscale" && o.Operation != "background-remove" {
		return fmt.Errorf("--operation must be upscale or background-remove")
	}
	generation := []string{"width", "height", "steps", "cfg-scale", "negative-prompt", "safe-mode"}
	upscale := []string{"scale", "enhance", "enhance-prompt", "enhance-creativity", "replication"}
	for _, flag := range o.Changed {
		if slices.Contains(generation, flag) && (o.Operation != "" || len(o.Inputs) > 0) {
			return fmt.Errorf("--%s is only supported for generation", flag)
		}
		if slices.Contains(upscale, flag) && o.Operation != "upscale" {
			return fmt.Errorf("--%s requires --operation upscale", flag)
		}
		if o.Operation != "" && slices.Contains([]string{"size", "aspect-ratio", "quality", "background", "edit-model"}, flag) {
			return fmt.Errorf("--%s is not supported for processing", flag)
		}
	}
	if o.Operation != "" {
		if hasPrompt {
			return fmt.Errorf("processing accepts no prompt")
		}
		if len(o.Inputs) != 1 || o.Inputs[0] == "-" || o.Inputs[0] == "clipboard" || mediautil.IsRemoteReference(o.Inputs[0]) {
			return fmt.Errorf("processing requires exactly one local input file")
		}
	}
	if slices.Contains(o.Changed, "edit-model") && (o.EditModel == "" || len(o.Inputs) == 0) {
		return fmt.Errorf("--edit-model requires editing inputs and a nonempty model")
	}
	if o.Upscale.Scale != nil && *o.Upscale.Scale <= 0 {
		return fmt.Errorf("scale must be positive")
	}
	for _, v := range []*float64{o.Upscale.EnhanceCreativity, o.Upscale.Replication} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return fmt.Errorf("enhancement values must be finite")
		}
	}
	return nil
}

func (o Options) controls() *media.VeniceControls {
	for _, flag := range []string{"width", "height", "steps", "cfg-scale", "negative-prompt", "safe-mode"} {
		if slices.Contains(o.Changed, flag) {
			return &o.Controls
		}
	}
	return nil
}

// Prepare validates capabilities before reading inputs, then returns a request
// closure for the existing presentation runner. Remote URLs are never fetched.
func (o Options) Prepare(provider media.ImageProvider, prompt string, read func(string) ([]byte, error)) (string, func(context.Context) (*media.ImageResult, error), error) {
	if err := o.Validate(prompt != ""); err != nil {
		return "", nil, err
	}
	if err := media.ValidateVeniceControls(provider, o.controls(), o.Size, o.AspectRatio); err != nil {
		return "", nil, err
	}
	if err := media.ValidateEditModel(provider, o.EditModel); err != nil {
		return "", nil, err
	}
	if o.Operation != "" {
		p, ok := provider.(media.ImageProcessingProvider)
		if !ok {
			return "", nil, fmt.Errorf("provider does not support image processing")
		}
		data, err := read(o.Inputs[0])
		if err != nil {
			return "", nil, err
		}
		img := media.InputImage{Path: o.Inputs[0], Data: data}
		if o.Operation == "upscale" {
			r := o.Upscale
			r.Image = img
			r.Debug = o.Debug
			r.DebugRaw = o.DebugRaw
			return "Upscaling image", func(ctx context.Context) (*media.ImageResult, error) { return p.Upscale(ctx, r) }, nil
		}
		return "Removing background", func(ctx context.Context) (*media.ImageResult, error) {
			return p.RemoveBackground(ctx, media.BackgroundRemovalRequest{Image: img, Debug: o.Debug, DebugRaw: o.DebugRaw})
		}, nil
	}
	if len(o.Inputs) == 0 {
		return "Generating image", func(ctx context.Context) (*media.ImageResult, error) {
			return provider.Generate(ctx, media.GenerateRequest{Venice: o.controls(), Prompt: prompt, Size: o.Size, AspectRatio: o.AspectRatio, Quality: o.Quality, Background: o.Background, Debug: o.Debug, DebugRaw: o.DebugRaw})
		}, nil
	}
	if !provider.SupportsEdit() {
		return "", nil, fmt.Errorf("provider does not support image editing")
	}
	if len(o.Inputs) > 1 && !provider.SupportsMultiImage() {
		return "", nil, fmt.Errorf("provider does not support multiple input images")
	}
	images := make([]media.InputImage, len(o.Inputs))
	for i, path := range o.Inputs {
		if mediautil.IsRemoteReference(path) {
			images[i].URL = path
		} else {
			images[i].Path = path
		}
	}
	if err := media.ValidateEditInputs(provider, images); err != nil {
		return "", nil, err
	}
	for i, path := range o.Inputs {
		if images[i].URL != "" {
			continue
		}
		data, err := read(path)
		if err != nil {
			return "", nil, err
		}
		images[i].Data = data
		if path == "clipboard" {
			images[i].Path = "clipboard.png"
		}
	}
	return "Editing image", func(ctx context.Context) (*media.ImageResult, error) {
		return provider.Edit(ctx, media.EditRequest{Model: o.EditModel, Prompt: prompt, InputImages: images, Size: o.Size, AspectRatio: o.AspectRatio, Quality: o.Quality, Background: o.Background, Debug: o.Debug, DebugRaw: o.DebugRaw})
	}, nil
}

// OutputName avoids consuming stdin or inventing a prompt for processing.
func (o Options) OutputName() string {
	if len(o.Inputs) == 0 {
		return o.Operation
	}
	name := filepath.Base(o.Inputs[0])
	return strings.TrimSuffix(name, filepath.Ext(name)) + "-" + o.Operation
}

// ValidateSize keeps the native omission selector CLI-only; the existing tool
// validator and schema retain their portable 1K/2K/4K contract.
func ValidateSize(size string) error {
	if size == media.NativeSize {
		return nil
	}
	return media.ValidateSize(size)
}

// RequestContext starts only after setup/local preparation. An explicit CLI
// timeout overrides the fallback; otherwise an existing caller deadline wins.
func (o Options) RequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.TimeoutExplicit {
		return context.WithTimeout(ctx, o.Timeout)
	}
	return mediautil.WithMediaTimeout(ctx)
}
