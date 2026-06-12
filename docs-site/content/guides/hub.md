---
title: "Hub"
weight: 16
description: "Run term-llm Hub: one dashboard over many term-llm web nodes, with server-side token injection and one-click open."
kicker: "Deploy agents"
---

`term-llm serve hub` runs the **term-llm Hub**: a launcher and control plane that puts every term-llm web node you operate behind one pane of glass. The dashboard shows each node with live reachability, latency, and (when detected) its agent name, version, and capabilities, and opens any node's full web UI through the hub with a single click.

```bash
term-llm serve hub
# term-llm Hub listening on http://127.0.0.1:8090
```

## Nodes

The core object is a **node**: a reachable term-llm serve (web/API endpoint) with an identity, a URL + base path, an optional web bearer token, and a source. Nodes are discovered from three resolvers, re-resolved on every request so changes are picked up without a restart:

1. **Static config** (`--config nodes.yaml`) — YAML or JSON:

   ```yaml
   nodes:
     - name: jarvis
       url: http://127.0.0.1:8081/chat
       token: <web bearer token>
     - id: edge
       url: https://edge.example.com
       base_path: /ui
       token: <token>
   ```

   `id` is derived from `name` when omitted; the base path may be embedded in the URL or given explicitly. Hub v1 requires a non-root base path such as `/chat` because path-based proxying needs a stable prefix to rebase.

2. **Contain workspaces** (on by default, disable with `--contain=false`) — every local `term-llm contain` workspace with a provisioned `WEB_TOKEN` in its `.env` shows up automatically, using its `WEB_PORT`/`WEB_BASE_PATH`.

3. **Dashboard-added nodes** — the **Add node** form (with a **Test connection** button) persists nodes to a local JSON store (`--nodes-file`, default `<data-dir>/hub/nodes.json`, mode 0600 since it holds tokens).

When two sources produce the same node id, precedence is config → local store → contain.

## Opening a node

Each node's **Open** action navigates to `/node/<id>/`, a reverse proxy onto that node's serve:

- The node's bearer token is injected **server-side**; tokens never reach the browser, and client-supplied `Authorization`, `Cookie`, and `X-Api-Key` headers are stripped before forwarding.
- The node UI's baked-in base path (`<base>` tag and `window.TERM_LLM_UI_PREFIX`) is rebased onto `/node/<id>` so the SPA's API calls, service worker, and subresources all route back through the hub.
- The hub injects `window.TERM_LLM_HUB` into the node's page, so the node's web UI shows a **Back to Hub** link in the sidebar (below Widgets).
- SSE and other long-lived streams pass through untouched; only connection and response-header times are bounded.

A node can also be made hub-aware when opened directly (not through the proxy):

```bash
term-llm serve web --hub-url http://127.0.0.1:8090/ --hub-node-id jarvis --hub-node-name Jarvis
```

## API

```text
GET    /api/nodes        nodes with probe status (never includes tokens)
POST   /api/nodes        add a node to the local store
DELETE /api/nodes/<id>   remove a local-store node
POST   /api/nodes/test   probe a node spec without persisting it
ANY    /node/<id>/...    reverse proxy to the node's serve
GET    /healthz          hub health
```

Probes hit each node's `{base}/healthz` with the node token. Serves report their agent name, version, and capabilities (`web`, `api`, `jobs`, `widgets`, `voice`) on `healthz` only to callers presenting the valid bearer token (or when the serve runs with auth disabled).

## Security posture

The hub is **experimental and loopback-only**: it has no authentication of its own yet, so it refuses to bind to a non-loopback host. Anyone who can reach the hub can reach every node it fronts. To expose it, put an authenticating reverse proxy in front and keep the hub itself on loopback. The backend transport never uses an environment proxy (`HTTP_PROXY` would see injected tokens). The hub rejects obvious cross-site browser requests and requires JSON content types for mutating node-registry APIs, but that is not a replacement for real hub auth.

Routing is path-based (`/node/<id>/...`) in v1; the proxy target is resolved per request, so host-based routing can be layered on later without changing the proxy plumbing. Because path routing puts hub UI and proxied node UI on the same browser origin, Hub v1 treats registered nodes/widgets as trusted. The node web UI namespaces localStorage by hub node id to avoid ordinary state bleed, but untrusted remote nodes/widgets still need the future host-based/widget-grant isolation work before they should be opened through a shared hub origin.

Future cross-node communication will be mediated by Hub-authenticated delegation APIs (reserved under `/api/delegations`) rather than by nodes calling each other directly. That follow-up should use distinct node→Hub credentials, default-deny delegation policy, and the existing jobs-v2 execution path on target nodes. Node self-registration, scheduling, hub-level user auth, cross-node delegation tools, and mTLS between hub and nodes are deliberately out of scope for v1.
