---
title: "Image generation"
weight: 3
description: "Generate, edit and process images through configured providers."
kicker: "Media"
source_readme_heading: "Image Generation"
next:
  label: Text embeddings
  url: /guides/text-embeddings/
---

```sh
term-llm image "a robot cat on a rainbow"
term-llm image "add a hat" -i photo.png
term-llm image "add sparkles" -i clipboard
term-llm image "robot cat" -o - | term-llm video "animate it" -i -
```

Images save under `~/Pictures/term-llm/`; interactive runs display through `icat` and copy to clipboard. Use `-o PATH` or `-o -` for bytes on stdout; `--no-display`, `--no-clipboard`, `--no-save` and `--no-spinner` control presentation. Paid image requests default to **one hour**, starting after CLI setup. `--timeout` explicitly shortens/extends it; caller deadlines are respected without hidden client caps. Flux polling shares this budget. Tools use the same provider fallback; synchronous legacy `code_mode` refuses images before dispatch—use direct `image_generate` or asynchronous `spawn_script`. Connection-establishment limits are separate; external proxy/harness caps remain uncertified. Local cancellation cannot undo remote charges.

### Providers and portable options

Select `--provider NAME[:MODEL]` or `image.provider`. Gemini is the default. API credentials use each `image.<provider>.api_key`, with its usual environment fallback:

| Provider | Environment | Editing |
|---|---|---|
| Gemini | `GEMINI_API_KEY` | Multiple inputs |
| OpenAI | `OPENAI_API_KEY` | Supported |
| ChatGPT | OAuth (`term-llm auth login chatgpt`) | One input |
| xAI | `XAI_API_KEY` | Unsupported |
| Venice | `VENICE_API_KEY` | Up to three inputs, model-dependent |
| Flux | `BFL_API_KEY` | Supported |
| OpenRouter | `OPENROUTER_API_KEY` | Multiple inputs |

`--size 1K|2K|4K`, `--aspect-ratio 16:9`, `--quality` and `--background` remain requests, not guarantees. OpenAI/ChatGPT support quality and background controls; Venice forwards `low|medium|high` quality, treats `auto` as unspecified and rejects `xhigh|max`. Other providers retain their existing translation/default behavior. ChatGPT defaults to GPT Image 2.5 Flare; `--provider chatgpt:gpt-image-2.5-sunburst` selects slower precision. Legacy language-model image settings migrate to Flare.

### Venice CLI capabilities

```sh
term-llm image "poster" -p venice --width 768 --height 1024 --steps 20 --cfg-scale 7 --negative-prompt blur --safe-mode
term-llm image "edit" -p venice -i https://example.org/image.png --edit-model EDITOR --size native
term-llm image "combine" -p venice -i local.png -i https://example.org/ref.png
term-llm image -p venice -i photo.png --operation upscale --scale 2 --enhance --enhance-prompt detail --enhance-creativity 0.3 --replication 0.5
term-llm image -p venice -i photo.png --operation background-remove
term-llm models --provider venice --type image --json
term-llm models --provider venice --type inpaint --json
```

Generation controls are typed and generation-only. Width/height must be paired and positive; pixel dimensions conflict with size/aspect ratio and suppress inherited resolution. Steps must be positive; numeric controls finite. New Venice controls, remote inputs and editor overrides error on unsupported providers rather than being ignored.

Editor precedence is `--edit-model` > `image.venice.edit_model` > the existing generation-model-derived editor (`-edit` suffix). `provider:model` still selects the generation model; use the explicit editor flag to override configured editing selection.

Processing requires exactly one **local file**, no prompt and no stdin/clipboard; it uses input-derived output names. Upscale-only options cannot accompany generation or background removal; processing rejects sizing/quality/background/editor flags. Background removal is not `--background transparent`.

HTTP(S) references pass through to Venice without local fetching. URLs reject credentials, fragments, whitespace and missing hosts. Single URL edits use JSON; mixed edits preserve raw base64 for local images, unlike video's data URLs. The `image_generate` tool remains local-only with its existing approvals; these new controls are CLI-only.

Typed listings use media credentials/root: `image.venice.api_key`, then `VENICE_API_KEY`; `image.venice.base_url`, then `providers.venice.base_url`, then public default. Untyped listing retains inference authority. Explicit listing alone refreshes the normalized-root cache, preserving other types' freshness. Image calls/completion never discover implicitly. Sizing now always preserves explicit/configured/default resolution, independent of cache freshness; cached native-size hints no longer silently discard it. For native-size models use `--size native`, including editing: it deterministically omits resolution. This CLI-only selector errors on other providers; default/requested tier sizes remain explicit. Missing/stale constraints do not reject requests. Catalog/error bodies are bounded to 8 MiB; image JSON allows base64-expanded 64 MiB plus 1 MiB metadata, with decoded/raw images bounded to 64 MiB; all Venice media/catalog redirects are refused. Verification is offline fixtures, not live-model certification.

### OpenAI-compatible servers

A named `providers` entry with `type: openai_compatible`, `base_url` (including `/v1` when required) and `model` can serve images through `--provider NAME`. Built-in names retain their native configuration. The adapter posts `/images/generations` with `model`, `prompt`, `n: 1`, `response_format: b64_json`; responses contain `data[].b64_json` or `url`. Size is sent only when requested; editing is unsupported. Optional keys use normal credential resolution; unrelated `OPENAI_API_KEY` is never forwarded. Use `base_url`, not chat-only `url`; term-llm bundles no server or model weights.
