---
title: "WebRTC direct routing"
weight: 7
description: "Bypass a relay server and connect the browser directly to a home-hosted term-llm instance over a WebRTC data channel."
kicker: "Direct connect"
next:
  label: Search
  url: /guides/search/
---
## The problem

When term-llm runs on a home machine behind NAT and the browser connects through a public relay (VPS, reverse proxy, Cloudflare Tunnel), every API request pays the relay's round-trip penalty. For a relay in a different region that can be 200-500 ms per request, which adds up during streaming conversations.

## How it works

WebRTC direct routing adds a peer-to-peer data channel between the browser and the home machine. After a brief ICE negotiation through a signaling server, all subsequent `/v1/` API traffic flows directly over UDP, bypassing the relay entirely.

```text
Browser  --(HTTPS signaling)-->  signaling server (public)
   |                                       ^
   |                                       | poll
   +----(WebRTC data channel, P2P)------>  term-llm (home, behind NAT)
```

The normal HTTPS path is unchanged. WebRTC is purely additive: if ICE negotiation fails (corporate firewall, symmetric NAT, etc.), the browser silently falls back to HTTPS within 8 seconds. No user action required.

## Prerequisites

You need an external signaling server accessible from both the browser and the home machine. The signaling server handles the one-time SDP exchange that bootstraps the WebRTC connection. It does not relay any API traffic.

term-llm does not include a signaling server. You need to run one yourself or use an existing service.

## Enable it

```bash
term-llm serve web \
  --webrtc \
  --webrtc-signaling-url https://signal.example.com/webrtc \
  --webrtc-token "$SIGNAL_TOKEN"
```

The browser UI shows a **lightning bolt** indicator when the data channel is active.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--webrtc` | `false` | Enable WebRTC direct routing |
| `--webrtc-signaling-url` | (none) | Signaling server base URL (must be HTTPS) |
| `--webrtc-token` | (none) | Bearer token for authenticating with the signaling server |
| `--webrtc-stun` | `stun:stun.l.google.com:19302` | STUN server URL(s), repeatable |
| `--webrtc-max-conns` | `10` | Maximum concurrent WebRTC connections |

## Configuration file

You can set the flags in `config.yaml` so you don't have to pass them every time:

```yaml
serve:
  webrtc:
    enabled: true
    signaling_url: https://signal.example.com/webrtc
    token: your-signaling-token
    stun_urls:
      - stun:stun.l.google.com:19302
    max_conns: 10
```

Then just run:

```bash
term-llm serve web --webrtc
```

## Security

WebRTC direct routing does not weaken the existing auth model:

- **Auth tokens travel inside the data channel.** Every request frame includes the same `Authorization: Bearer` header that HTTPS requests carry. The existing auth middleware validates it.
- **DTLS encrypts the data channel.** The ICE connection is upgraded through DTLS with certificate fingerprint verification against the SDP offer, preventing man-in-the-middle attacks even if the signaling server is compromised.
- **Path validation.** Only paths under `{basePath}/v1/` are dispatched. Requests for static assets, admin endpoints, or path-traversal attempts are rejected before reaching the HTTP handler.
- **Body size limit.** Request bodies larger than 10 MB are rejected.
- **Connection cap.** The `--webrtc-max-conns` flag limits concurrent WebRTC connections to prevent resource exhaustion.
- **HTTPS-only signaling.** term-llm refuses to start if the signaling URL is not HTTPS.

## How the signaling protocol works

The signaling server needs to implement two endpoints:

1. **`POST /session`** — creates a session and returns `{ "session_id": "..." }` along with optional STUN/TURN credentials.
2. **`POST /signal`** and **`GET /signal`** — exchange SDP offers and answers keyed by session ID.

The browser creates a WebRTC offer, posts it via the signaling server. term-llm polls for offers, generates an answer, and posts it back. Once both sides have each other's SDP (which includes ICE candidates), they establish a direct UDP connection. After that, the signaling server is no longer involved.

## Troubleshooting

**No lightning bolt in the UI** — WebRTC negotiation failed silently. Check:
- The signaling URL is reachable from both the browser and the home machine
- The signaling token is correct
- STUN servers are reachable (corporate firewalls sometimes block UDP)
- Both sides can reach each other via UDP (symmetric NAT on both sides will prevent direct connections without a TURN relay)

**Works locally but not remotely** — you likely need a TURN relay for NAT traversal. Add a TURN server URL via `--webrtc-stun`:

```bash
term-llm serve web --webrtc \
  --webrtc-signaling-url https://signal.example.com/webrtc \
  --webrtc-stun "turn:turn.example.com:3478?transport=udp"
```

**High latency despite direct connection** — verify with browser DevTools that the ICE candidate pair is using a direct host or server-reflexive candidate, not a relay candidate.

## Related pages

- [Web UI and API](/guides/web-ui-and-api/)
- [Configuration](/reference/configuration/)
