# Docker Agent Containers

Scaffold and run independent term-llm agents — one container per agent, fully isolated, no image rebuild needed.

## Quick start

```bash
./init.sh myagent
cd myagent
# edit .env — add at least one API key
docker compose up -d
```

Web UI at `http://localhost:8081/chat`.

## Documentation

See the full [Agent Containers guide](../docs-site/content/guides/agent-containers.md) for configuration, personality setup, memory wiring, multiple agents, and updating.
