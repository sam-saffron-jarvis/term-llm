package cmd

import (
	"github.com/spf13/cobra"
	imageapp "github.com/term-llm/term-llm/internal/app/image"
)

func captureImageOptions(cmd *cobra.Command) imageapp.Options {
	o := imageapp.Options{Operation: imageOperation, EditModel: imageEditModel, Timeout: imageTimeout, TimeoutExplicit: cmd.Flags().Changed("timeout"), Inputs: imageInputs, Size: imageSize, AspectRatio: imageAspectRatio, Quality: imageQuality, Background: imageBackground, Debug: imageDebug, DebugRaw: debugRaw}
	for _, name := range []string{"edit-model", "width", "height", "steps", "cfg-scale", "negative-prompt", "safe-mode", "scale", "enhance", "enhance-prompt", "enhance-creativity", "replication", "size", "aspect-ratio", "quality", "background"} {
		if cmd.Flags().Changed(name) {
			o.Changed = append(o.Changed, name)
		}
	}
	if cmd.Flags().Changed("width") {
		v := imageWidth
		o.Controls.Width = &v
	}
	if cmd.Flags().Changed("height") {
		v := imageHeight
		o.Controls.Height = &v
	}
	if cmd.Flags().Changed("steps") {
		v := imageSteps
		o.Controls.Steps = &v
	}
	if cmd.Flags().Changed("cfg-scale") {
		v := imageCFG
		o.Controls.CFGScale = &v
	}
	if cmd.Flags().Changed("negative-prompt") {
		v := imageNegative
		o.Controls.NegativePrompt = &v
	}
	if cmd.Flags().Changed("safe-mode") {
		v := imageSafeMode
		o.Controls.SafeMode = &v
	}
	if cmd.Flags().Changed("scale") {
		v := imageScale
		o.Upscale.Scale = &v
	}
	if cmd.Flags().Changed("enhance") {
		v := imageEnhance
		o.Upscale.Enhance = &v
	}
	if cmd.Flags().Changed("enhance-prompt") {
		v := imageEnhancePrompt
		o.Upscale.EnhancePrompt = &v
	}
	if cmd.Flags().Changed("enhance-creativity") {
		v := imageCreativity
		o.Upscale.EnhanceCreativity = &v
	}
	if cmd.Flags().Changed("replication") {
		v := imageReplication
		o.Upscale.Replication = &v
	}
	return o
}
