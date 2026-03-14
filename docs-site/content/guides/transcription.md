---
title: "Transcription"
weight: 8
description: "Transcribe audio files to text with OpenAI, Mistral Voxtral, a local Whisper server, or whisper.cpp CLI."
kicker: "Audio"
featured: true
next:
  label: Image generation
  url: /guides/image-generation/
---
## Basic usage

```bash
term-llm transcribe meeting.m4a
```

Supported input extensions include:

- `.ogg`
- `.mp3`
- `.wav`
- `.m4a`
- `.flac`
- `.mp4`
- `.webm`

## Useful flags

```bash
term-llm transcribe interview.mp3 --language en
term-llm transcribe note.m4a --provider openai
term-llm transcribe memo.wav --provider mistral
term-llm transcribe call.ogg --provider whisper-cli --porcelain
```

Key options:

- `--language` for a language hint such as `en` or `ja`
- `--provider` to select the transcription backend
- `--porcelain` to output only transcript text

## Provider options

term-llm supports several transcription backends:

- `openai`
- `mistral` (Voxtral)
- `local` for a local Whisper-compatible server
- `whisper-cli` for `whisper.cpp`

If you omit `--provider`, term-llm uses the configured transcription provider or falls back to OpenAI.

## whisper.cpp CLI mode

For `--provider whisper-cli`, term-llm looks for a `whisper` binary in `PATH` and a model file via:

- `WHISPER_MODEL`
- `providers.local_whisper.model` in config
- common default model paths

Example:

```bash
export WHISPER_MODEL=~/.local/share/whisper/models/ggml-base.bin
term-llm transcribe note.m4a --provider whisper-cli
```

## When to use it

Use transcription when you want to:

- turn voice notes into text before summarizing or editing
- capture meeting audio for later analysis
- feed transcripts into `ask`, `edit`, or your own downstream tooling

## Related pages

- [Web UI and API](/guides/web-ui-and-api/)
- [Usage](/guides/usage/)
