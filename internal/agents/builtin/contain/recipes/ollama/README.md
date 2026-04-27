# ollama

Ollama local LLM runtime. Models live in a named volume so they survive
`rebuild`. Host port is `OLLAMA_PORT` (default 11434) and is bound to
`127.0.0.1` only by default.

## GPU

The CPU path works out of the box. For Nvidia GPU acceleration uncomment the
`deploy.resources.reservations.devices` block in `compose.yaml`. The host
needs the [nvidia-container-toolkit](https://github.com/NVIDIA/nvidia-container-toolkit)
installed and configured for Docker.

## Pull a model

```sh
term-llm contain exec <name> -- ollama pull llama3.2
```

## Hit the API

```sh
curl http://localhost:${OLLAMA_PORT:-11434}/api/tags
```
