---
title: "Profiling and pprof"
weight: 7
description: "Connect to a running term-llm pprof server for CPU, heap, goroutine, block, and mutex profiling."
kicker: "Profiling"
next:
  label: Usage tracking
  url: /reference/usage-tracking/
---
## Start term-llm with profiling enabled

```bash
term-llm chat --pprof
term-llm chat --pprof=6060
TERM_LLM_PPROF=1 term-llm chat
```

Then, from another terminal:

```bash
term-llm pprof cpu
term-llm pprof heap
term-llm pprof goroutine
term-llm pprof block
term-llm pprof mutex
term-llm pprof web
```

## Commands

- `pprof cpu [PORT]` capture a CPU profile
- `pprof heap [PORT]` capture a heap profile
- `pprof goroutine [PORT]` dump goroutine stacks
- `pprof block [PORT]` capture a blocking profile
- `pprof mutex [PORT]` capture a mutex contention profile
- `pprof web [PORT]` open the pprof index in a browser

If you omit the port, term-llm tries to auto-discover it from the pprof registry.

## CPU duration

```bash
term-llm pprof cpu --duration 10
```

Default CPU capture duration is 30 seconds.

## When to use it

Use pprof when you are debugging:

- CPU hotspots
- memory growth
- goroutine leaks
- contention and blocking behavior

## Related pages

- [Debugging](/guides/debugging/)
- [Usage tracking](/reference/usage-tracking/)
