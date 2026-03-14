---
title: "Image generation"
weight: 3
description: "Generate and edit images with Gemini, OpenAI, xAI, Venice, or Flux."
kicker: "Media"
source_readme_heading: "Image Generation"
next:
  label: Text embeddings
  url: /guides/text-embeddings/
---
Generate and edit images using AI models from Gemini, OpenAI, or Flux (Black Forest Labs).

```bash
term-llm image "a robot cat on a rainbow"
```

By default, images are:
- Saved to `~/Pictures/term-llm/` with timestamped filenames
- Displayed in terminal via `icat` (if available)
- Copied to clipboard (actual image data, pasteable in apps)

### Image Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--input` | `-i` | Input image to edit |
| `--provider` | | Override provider (gemini, openai, flux) |
| `--output` | `-o` | Custom output path |
| `--no-display` | | Skip terminal display |
| `--no-clipboard` | | Skip clipboard copy |
| `--no-save` | | Don't save to default location |
| `--debug` | `-d` | Show debug information |

### Image Examples

```bash
# Generate
term-llm image "cyberpunk cityscape at night"
term-llm image "minimalist logo" --provider flux
term-llm image "futuristic city" --provider xai  # uses Grok image model
term-llm image "watercolor painting" -o ./art.png

# Edit existing image (not supported by xAI)
term-llm image "add a hat" -i photo.png
term-llm image "make it look vintage" -i input.png --provider gemini
term-llm image "add sparkles" -i clipboard       # edit from clipboard

# Options
term-llm image "portrait" --no-clipboard        # don't copy to clipboard
term-llm image "landscape" --no-display         # don't show in terminal
```

### Image Providers

| Provider | Models | Environment Variable | Config Key |
|----------|--------|---------------------|------------|
| Gemini (default) | gemini-2.5-flash-image | `GEMINI_API_KEY` | `image.gemini.api_key` |
| OpenAI | gpt-image-1, gpt-image-1.5, gpt-image-1-mini | `OPENAI_API_KEY` | `image.openai.api_key` |
| xAI | grok-2-image-1212 | `XAI_API_KEY` | `image.xai.api_key` |
| Venice | nano-banana-pro | `VENICE_API_KEY` | `image.venice.api_key` |
| Flux | flux-2-pro, flux-2-max, flux-kontext-pro | `BFL_API_KEY` | `image.flux.api_key` |
| OpenRouter | various | `OPENROUTER_API_KEY` | `image.openrouter.api_key` |

Image providers use their own credentials, separate from text providers. This allows using different API keys or accounts for text vs image generation.

**Note:** xAI and Venice image generation do not support image editing (`-i` flag).
