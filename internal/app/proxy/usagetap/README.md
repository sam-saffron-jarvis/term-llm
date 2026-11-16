# Response usage parser

`usagetap` observes response bytes only. It has no networking, credentials,
pricing, persistence or request-body access, and does not modify forwarded bytes.

```go
p := usagetap.ForResponse("/v1/responses", "text/event-stream")
p.Feed(chunk)
usage := p.Finish(transportCompleted)
```

Instances are single-owner. `Snapshot` returns a value; `Finish` is idempotent.
`FeedEvent` accepts decoded events; `NewWebSocket` observes uncompressed server
frames. Do not mix event feeds with buffered partial byte feeds.

JSON bodies, SSE events, NDJSON lines and assembled frames are bounded to 1 MiB.
Responses retain at most 128 IDs; repeated IDs replace snapshots, distinct IDs
sum. Missing IDs cannot establish multiple turns. Overflow, malformed identities,
unsupported encoding/compression or lost observability remains unknown.

Absent counts are `UnknownTokens` (`-1`), not zero. `Source` distinguishes reported,
partial, unknown and not-applicable evidence; reported does not guarantee every
count exists. OpenAI/Gemini input includes cached tokens; Anthropic input excludes
its separate cache reads/writes. OpenAI reasoning is included in output; Gemini
thoughts are separate. Normalize downstream without double-counting. Models and
tiers are exact upstream assertions, never price guesses.

`proxy.NewUsageObserver` handles concurrent transport reads/close, panic isolation
and publishing before terminal persistence. Close before EOF is conservative
partial, including WebSocket closure. Counts are not bills; no monetary charge
adapter is implemented. Protocol, boundary, replay and cancellation tests define
the detailed behavior.
