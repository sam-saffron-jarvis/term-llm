---
title: "Video generation"
weight: 4
description: "Quote, queue and recover Venice video jobs."
kicker: "Media"
---

```sh
term-llm video "a corgi surfing at sunset"
term-llm video "animate it" -i photo.png --aspect-ratio 9:16
term-llm video "combine references" -i https://example.org/start.png -r pose.png -r https://example.org/ref.png
term-llm video "astronaut on mars" --model MODEL --quote-only --json
term-llm video "city lights" --model MODEL --skip-quote --no-wait --json
term-llm video --model MODEL --queue-id ID --no-wait --json
term-llm video --model MODEL --queue-id ID -o recovered.mp4
```

Models in these examples are caller-selected, not availability guarantees. Defaults remain `longcat-distilled-text-to-video` or, with `--input`, `longcat-distilled-image-to-video`; duration remains `5s`. Normally the command quotes, queues once, polls, and saves under `~/Pictures/term-llm/`. `--quote-only` never reads input files; `--skip-quote` explicitly bypasses pricing. Neither permits automatic queue retries.

**Changed defaults:** absent `--resolution` and `--negative-prompt` now omit those fields instead of forcing 720p and a canned prompt. Provider defaults can change price; inspect the quote. Explicit `--audio=false` and `--negative-prompt=''` remain explicit. Results omit unrequested resolution. Durations accept positive seconds and resolutions accept forms such as `360p` or `4k`; actual model support remains authoritative. Fresh explicitly cached constraints reject known unsupported choices; missing/stale metadata never blocks or triggers discovery.

For Seedance, `--seedance-consent` attests to terms/privacy, legal rights and screening acknowledgement for this queue only. Consent is never inferred, persisted, or claimed by a quote.

### Recovery and storage

`--queue-id` requires `--model`, rejects prompts and explicitly supplied generation flags, and never quotes, queues or reads original inputs. `--no-wait` retrieves status once in this mode; otherwise polling uses `--poll-interval` and `--timeout`.

The queue receipt always appears immediately on stderr, including with `--json`. JSON stdout remains one document; timeout, cancellation, retrieve/save failure preserve `model`, `job.queue_id`, `status` and a shell-quoted `recovery` command, with nonzero exit. A queue connection failure may already have created a paid job: **do not requeue blindly**. Unknown statuses remain visible until the deadline; invalid statuses and failed responses stop.

Remote deletion is **off by default**. Output is preflighted before spending; downloads keep remote media and stream to a same-directory temporary file, bounded to 1 GiB, then sync and rename. `--delete-remote` calls the documented `/video/complete` endpoint only after durable local success. Cleanup failure warns with job identity without invalidating the saved result. JSON/catalog responses are capped at 8 MiB; all Venice media/catalog redirects are refused.

### Inputs and configuration

`--input` and repeatable `--reference` accept local files, HTTP(S) URLs, or stdin (`-`, once). With media on stdin, supply the prompt as an argument. Remote references pass through to Venice, never a local fetcher; local video inputs use data URLs. URLs must have a host and no credentials, fragments or whitespace. Model-specific advanced fields remain unsupported.

Video and typed `models --provider venice --type video --json` use `image.venice.api_key` (then `VENICE_API_KEY`) and `image.venice.base_url` (then `providers.venice.base_url`, then public default). Untyped model listing retains inference credentials. Explicit typed listing alone refreshes root-scoped media metadata; operations/completion never fetch catalogs. These contracts have offline fixture coverage, **not live-provider certification**.
