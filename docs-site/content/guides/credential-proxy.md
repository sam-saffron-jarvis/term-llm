---
title: "Credential proxy"
description: "Keep provider credentials on the proxy; connect clients with revocable tokens."
weight: 45
---

`term-llm serve proxy` forwards HTTP/SSE and opt-in WebSockets to fixed upstreams,
substituting server-held credentials. It runs independently of chat and provider
configuration. Saving routes does not probe vendors. Each client has its own token and
provider approvals; admin sessions never authorize forwarding.

## Quick start: local passkey administration

```sh
term-llm serve proxy
```

1. Open **http://localhost:8082** on the same computer. Use `localhost`, not an IP:
   passkeys bind to the hostname.
2. Run the printed `cat` command, paste the bootstrap token, name your passkey and
   enroll. The token expires ten minutes after startup; restart if it expires.
3. Open **Providers → Add provider** and choose a preset. The preset asks for only
   what it needs: sign in for ChatGPT or [Grok](#share-a-grok-subscription), [sign in once for Claude Code](#set-up-a-shared-claude-subscription),
   [sign in once for personal Antigravity](#run-personal-antigravity-through-the-proxy),
   or add an API key for a service like Venice.
4. Select the new provider in the **Providers** list. On its **Access** tab,
   choose **New client** (or tick existing clients and choose **Approve selected**).
   That gives an app or device access to every route the provider currently has. For Claude Code and Antigravity, use the
   device launcher instead, after the shared subscription is ready. Save the
   shown-once client token privately. In an app that accepts a custom API root,
   use the route URL and the client token, **not** the upstream credential. The **Clients** tab lets you see and manage
   which providers a client is approved for.

The listener defaults to `127.0.0.1:8082`. Keep its state directory across restarts.
Remote administration requires a stable HTTPS hostname and explicit enrollment.

## Providers and client approvals

A provider is a stored entry with a unique name, a display label, and one or
more routes. Use the exact name, not the label or family, in `proxy.providers`.
The admin UI also names providers by this name (client lists, approval
checkboxes and usage); the label is only a description shown beside it, because
several providers can share a preset's default label, such as `anthropic` and
`anthropic-work` both labelled **Anthropic**. The cost timeline's provider
grouping shows route names, so a multi-route provider such as `chatgpt` can
appear as `chatgpt` and `chatgpt-backend`.
Presets group routes that share a key or account: for example, Exa REST and Exa
MCP are two routes of provider `exa`. Each provider belongs to one family:
**Inference**, **Search**, **Media**, **Embeddings** or **Classification**. The
family groups providers; it does not limit which features their routes serve.
Search, media, embeddings and classification presets use their canonical
provider names. Inference can have separately named providers such as
`chatgpt-work`.

The server computes one status dot across a provider's routes, including subpath
rules. If several conditions apply, the first matching row wins:

| Dot | Reason |
| --- | --- |
| Red | **API key missing** (even on a disabled route), **Sign-in needed** (account not ready or refreshing, or a token-only account has expired), or **Migration needed** (legacy native account), in that order. |
| Grey | **Disabled** — all routes are disabled. |
| Orange | **Refreshing sign-in**, **Unencrypted upstream** (an enabled route uses HTTP outside loopback), or **Partly disabled**, in that order. |
| Green | None of the above. |

The dot describes the active configuration, not upstream connectivity or client
approval. Save and reload errors are reported separately.

**Providers** lists every provider by family with its dot, any reason, and how
many clients have access; a filter appears once the list is long. Selecting a
provider opens its tabs: **Access** (approve, create or remove clients),
**Connection** (routes, upstream credentials and accounts), **Client config**
(inference providers' term-llm settings) and **Settings** (label and
**Delete provider**). Each route's facts say which credential the client sends
and which one the proxy adds upstream.

Client approval applies to the whole configured provider. Approving a client
authorizes every route the provider has at that moment; it never authorizes a
different provider or a route added later. If you add a route to the provider or
change a route's upstream authority, access policy or credential rules, that
route needs approval again, and the client is marked as needing approval.
Existing clients created with per-route grants keep exactly the grants they
already have; they are not expanded automatically. Approve the provider when you
want those clients to cover all of its current routes. Removing approval removes
the client's grants for the provider's current routes; its token and approvals
for other providers stay unchanged.

Grouping never widens access and does not change routing. An **OpenAI API**
provider keeps its own API key and approval even when it targets
`api.openai.com` like the ChatGPT OAuth embeddings route. Existing `?tab=routes`
bookmarks still open **Providers**. The public management API is route-based.

## Share a ChatGPT subscription

1. Choose **Providers → Add provider → ChatGPT**, then **Sign in to ChatGPT**.
2. Open the OpenAI sign-in page from the proxy, enter the one-time code and
   approve the request. Return to the proxy. It stores one login and creates the
   ChatGPT routes together when their names and prefixes are available. No local
   OAuth credential file is required. Your OpenAI account must allow device-code
   sign-in.
3. Select **chatgpt** in the **Providers** list and use its **Access** tab to
   authorize an app or device for the provider's current routes. A successful provider
   sign-in does not give any new client access by itself.

If your workspace disables device-code sign-in, **Advanced** in ChatGPT setup
lets you import credentials manually, even on a new proxy.

Use **Reconnect ChatGPT** on the provider's **Connection** tab to renew the login without changing client
approvals. Use a distinct name when setting up a separate ChatGPT account. The
main route is enough for chat; the other routes cover usage, dictation,
embeddings, and realtime sideband, and one approval covers all of them. If a
route is added later, approve the provider again for clients that need it. A
ChatGPT subscription is separate from the **OpenAI API** key preset. To route
only ChatGPT through the proxy in term-llm, [configure the client allowlist](#using-term-llm-as-a-client)
with `providers: [chatgpt]`.

## Share a Grok subscription

1. Choose **Providers → Add provider → Grok**, then **Sign in to Grok**.
2. Open the xAI verification page, enter the one-time code and approve the
   request. The proxy stores the login and creates one Grok plan route (`/grok/`
   by default).
3. On the provider's **Access** tab, approve the apps or devices that should use the plan.
   With the proxy URL and client token configured, try `term-llm models --provider grok`.
   If the client has a `proxy.providers` allowlist, include `grok`.

Neither the proxy nor its clients need the Grok Build CLI or a local Grok login.
You don't need to paste tokens. If the proxy already holds a Grok login, you can
choose **Advanced → Use a saved account** instead of signing in again.
`grok-bin` is a separate adapter for running that CLI; new proxy setups don't
create a route for it. Existing `grok-bin` routes and their approvals are
unchanged. Choose **xAI API** instead if you want to use a developer API key.

**Connection → Reconnect Grok** renews the same account, including for file-managed
routes, without changing approvals. If a Grok account was already signed out
before the proxy began retaining its identity, it cannot verify a reconnect.
Create a separately named provider for the account and explicitly approve the
clients that need it. For file-managed routes, have the operator make the change.

## Run native Claude Code through the proxy

Claude Code can opt in to proxy-held credentials. The launcher never modifies
ordinary `claude`: your regular `~/.claude` settings, credentials, hooks and plugins
are untouched, and a plain `claude` run continues to use its own local login.

### Set up a shared Claude subscription

Claude Code routes use a subscription that you sign in to as the proxy operator.
Clients share its limits, so only approve devices you trust.

1. Choose **Providers → Add provider → Claude Code** and click
   **Create shared route & sign in**.
2. Choose the client to sign in from, usually the one on your own device, or
   name a new client. The proxy does not create a separate launcher client. An
   existing client keeps its token and other approvals, and gets access to the
   new route. Do not choose a worker's client.
3. Copy the **Operator sign-in command** and run it on that device with an
   updated `term-llm`. For an existing client, first set `TERM_LLM_PROXY_TOKEN`
   to that client's token, or replace it in the command with the token or a
   credential reference. Start within 15 minutes and finish the Claude browser
   sign-in. If Claude does not ask you to sign in, run `/login`. The proxy holds
   the subscription credentials; workers do not get them.
4. Use **Refresh** on
   **Providers** after finishing sign-in. Once the shared subscription is ready, select
   the provider and click **Add device** under **Shared subscription** on its **Access** tab.
   Run that command on the worker with an updated `term-llm`. The worker uses the
   proxy’s shared sign-in without opening a browser. Issue a separate launcher
   for each worker so you can remove its access without signing out Claude or
   affecting other workers.
5. If the shared sign-in needs attention, click
   **Sign in to shared subscription** (or **Reconnect**) on the same tab for the same
   route, then choose the client to sign in from. Finish sign-in and refresh **Providers**. **Add device** does not sign
   in or reconnect the subscription, and is unavailable while it is not ready.

Remove a worker’s access with **Remove** in the provider’s **Access** tab, or
revoke its client in **Clients**. Do not use **Delete provider** (on **Settings**)
to remove just one worker: it removes all Claude routes, grants and saved proxy
sign-ins.

### Optional: use the same connection with term-llm

The copied launcher command works without changing any configuration file. For
term-llm's `claude-bin` provider, copy the optional provider YAML shown with it.
For example, using a private token file:

```yaml {title="config.yaml"}
providers:
  claude-bin:
    proxy:
      url: https://proxy.example.test/claude-bin/
      token: file:///run/secrets/claude-proxy-token
```

`url` is the Claude route root supplied with the launcher, not the bare proxy origin.
Set `token` to a credential reference for that client's scoped proxy token, not an
Anthropic credential. This provider-local block redirects only `claude-bin`.
If you also use a [top-level `proxy:` block](#using-term-llm-as-a-client), list
only the other providers there (for example, `providers: [chatgpt, venice]`).
Do not list `claude-bin` in both places.

```sh
term-llm proxy run claude
term-llm proxy run claude -- --resume
```

The launcher is opt-in per invocation. Supply both `--url` and `--token` to use a
different route root and scoped token for one run; they do not create a route or
provider approval.
Everything after `--` is passed to native Claude. Prefer a `file://` token reference
over a literal value on the command line.

Only the operator launcher signs in. The proxy intercepts the HTTPS token
exchange on `platform.claude.com` and encrypts the real OAuth tokens. It refreshes
the shared login and gives each client launcher a scoped placeholder, not a
reusable Anthropic credential.

The launcher sets a private, term-owned `CLAUDE_CONFIG_DIR` for the child, so normal
Claude state stays separate. The proxy CA is fetched for that launch and trusted
only through the process's `NODE_EXTRA_CA_CERTS`; it is not installed in the
operating-system trust store, and TLS verification is never disabled. Competing
vendor credentials, cloud auth switches and ambient proxy settings are removed
from the child environment. The launcher sets its own `HTTP_PROXY` and
`HTTPS_PROXY` for Claude and marks routing as host-managed so Claude settings
cannot replace them. The calling shell's proxy settings are unchanged.

The tunnel is not a general web proxy or MITM. Only `api.anthropic.com:443` and
`platform.claude.com:443` are accepted. The public startup checks `GET/HEAD
/api/hello` and `GET/HEAD /v1/oauth/hello` work before Claude sign-in; they still
require a granted proxy client, send no credentials upstream, and check the real
upstream response. Other platform traffic is limited to the fixed token endpoint.
On the API host, the proxy forwards validated API-host paths over the authenticated
native tunnel using the operator’s OAuth session, subject to the shared-session
endpoint restrictions and any route Allow rules. Anthropic still decides OAuth
permissions and response status. Platform token exchange remains limited to the
fixed token endpoint, and the public startup probes stay credentialless and
restricted to their fixed paths. There is no credentialless API pass-through or expansion to other
providers. Loopback and safe existing `NO_PROXY` entries stay direct so local tools
and MCP servers keep working; other HTTPS destinations sent through the proxy are
refused. Each connection lasts at most one hour. An intervening reverse proxy must
support HTTP/1.1 CONNECT; otherwise point the launcher at the credential proxy
listener directly. Revoking a client or grant closes its open tunnels. The worker
loses access, but the shared subscription and other workers stay connected. A
compromised worker can still use its granted route and isolated profile; this
is not an OS sandbox.

`term-llm usage -p claude-bin` uses Claude's native `get_usage` control request
through the same binding and does not fall back to local vendor credentials. The **Billing** selector only
categorizes spending records; it does not change login permissions, provider charges
or limits.

If an older release installed its connector settings into your Claude profile, use
`term-llm proxy disconnect claude` once to undo only the settings that connector
owned. The old connector and its browser-approval flow are retired; the native
launcher does not use them and never edits your Claude settings.

## Run personal Antigravity through the proxy

This setup is for the **Antigravity personal subscription**, not Antigravity
Enterprise and not the Gemini API key preset. Operator sign-in and the standalone
`term-llm proxy run agy` launcher require Linux and `agy 1.5.12` or newer.
The bound `agy-bin` provider also runs on macOS with `agy 1.5.13` or newer and
`/usr/bin/sandbox-exec`. Use HTTPS for remote proxy URLs; plain HTTP is accepted
only on loopback.

1. Choose **Providers → Add provider → Antigravity**, then
   **Create shared route & sign in**. The route uses the fixed
   personal service `https://daily-cloudcode-pa.googleapis.com`.
2. Choose the client to sign in from, or name a new one, as for Claude Code.
   Run the **Operator sign-in command** on your Linux device with
   an updated `term-llm`, using that client's token. This command ends in
   `-- acp --login`. Follow the prompt within 15 minutes
   to sign in with your personal Google account. If `agy` prints a sign-in link,
   open it in your own browser.
3. Refresh **Providers**
   after signing in. Once the shared subscription is ready, select the provider
   and click **Add device** on its **Access** tab to issue a separate
   launcher for each worker. Run it as shown, without `-- acp --login`. Workers
   use the shared subscription and its limits without signing in to Google.
4. Click **Sign in to shared subscription** or **Reconnect** on the **Access** tab if
   the subscription needs attention. Remove a worker’s provider approval or
   revoke its client token to remove only its access. **Settings → Delete provider**
   removes all Antigravity proxy routes, their grants and saved proxy sign-ins.
   Other providers, request history and your normal Antigravity setup are unchanged.

For term-llm's `agy-bin` provider, use the optional provider YAML shown with the
launcher. For example:

```yaml
providers:
  agy-bin:
    proxy:
      url: https://proxy.example.test/agy-bin/
      token: file:///run/secrets/agy-proxy-token
```

Use the worker’s route URL and scoped proxy token, not the bare proxy origin or
a Google credential. Once the operator has completed sign-in, run a prompt
through the bound provider on Linux or macOS:

```sh
term-llm ask -p agy-bin 'Hello'
```

On macOS, the child uses a private OAuth file and can only connect over IP to
loopback, where term-llm runs its rewrite proxy and tool bridge. It does not
use your login Keychain. On Linux you can also run `term-llm proxy run agy`
to start a standalone native worker.

The launcher isolates native credential state from normal `agy` use. The proxy
handles the operator’s OAuth exchange and refresh through its provider-scoped
tunnel: real Google OAuth tokens stay in the proxy vault, while the client
receives opaque tokens bound to its grant, route and account slot. The tunnel is limited to the personal
service, token exchange on `oauth2.googleapis.com`, and the specific user-info
endpoint `GET /oauth2/v2/userinfo` on `www.googleapis.com`. It does not proxy
arbitrary Google APIs or browser traffic. See [Forwarding behavior](#forwarding-behavior) for the worker
isolation requirements.

On Linux and macOS, `term-llm usage -p agy-bin` runs agy's read-only `/usage`
command through the same bound worker path as inference, in a fresh private
profile. It reports the shared subscription's quota and does not fall back to
your local agy login.

### Upgrade an existing native route

The proxy’s schema v8 upgrade keeps routes, client tokens, grants, and existing
shared Claude sign-ins. It does not turn any old per-device Claude Code or
Antigravity sign-in into a shared login. For each converted account, open
**Providers → Access → Sign in to shared subscription** for its provider and complete one new operator
sign-in. Refresh **Providers** afterward. The operator sign-in restores access
for existing clients; workers do not need new tokens or a vendor login. New
worker launchers and approvals are unavailable until the shared route is enabled
and ready.

## Provision clients with an admin API key

In **Admin → API keys**, create a named key and save the token when shown.
Send the key as a Bearer token to `/_proxy/manage/v1/` over **loopback or HTTPS
only**. Keep it in a secret store; do not use it from browser code.

A key can create clients for **any** grantable route, including routes added
later. Any active key can list, regrant, revoke or purge API-created clients,
regardless of which key created them. Keys cannot see or change browser-created
clients, modify routes, access secrets, accounts, history, money, debug,
passkeys or native provider setup, authorize forwarding, or sign in to the browser UI.
Revoking a key stops provisioning immediately. Select **also revoke clients
created by this key** to revoke its clients too; otherwise those tokens keep
working under their existing grants.

Browser provider approvals apply only to browser-managed clients. The public
management API remains route-based: create and regrant API-created clients with
explicit route grants. Approving a provider in the browser never widens an
API-created client, and adding a route to a provider never adds it to a client
automatically.

Fetch the route names and current configuration `revision` first:

```sh
api=https://proxy.example.test/_proxy/manage/v1
curl -fsS -H "Authorization: Bearer $TERM_LLM_PROXY_ADMIN_KEY" "$api/routes"
```

Use that revision (replace `7` below) to create the client. Save the returned
`token` privately; give the app that client token, not the admin key.

```sh
curl -fsS -H "Authorization: Bearer $TERM_LLM_PROXY_ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Build worker","grants":["venice"],"revision":7,"external_id":"build/worker-1"}' \
  "$api/clients"
```

To replace grants, `PUT /clients/{id}/grants` with `grants` and the current
`revision`. If the revision is stale (`409`), fetch routes again and check the
intended grants before retrying. To recover from a lost create response, look
up the client by `external_id`; the API cannot recover its token. Revoke the
active client (`revoked: false`) and recreate it with the same `external_id`. A duplicate active
`external_id` returns `409` with `client_id`.

```sh
curl -fsS -G -H "Authorization: Bearer $TERM_LLM_PROXY_ADMIN_KEY" \
  --data-urlencode 'external_id=build/worker-1' "$api/clients"
curl -fsS -X DELETE -H "Authorization: Bearer $TERM_LLM_PROXY_ADMIN_KEY" \
  "$api/clients/$client_id"
```

Use `DELETE /clients/{id}?purge=1` to remove an already revoked client. Purging
leaves request and money history intact.

## Provider presets and client settings

Choose the preset from **Providers → Add provider**. Keep provider sign-in and client
access separate: provider credentials let the proxy call the vendor, while
approving a provider lets a particular app or device call the proxy through each
of that provider's current routes. For a service
that uses an API key, you can select a saved key or add one while setting up the
route. The setup guide disappears after the first recorded request and stays
complete across restarts and history cleanup. Route YAML download and
replacement live under **Admin**, not Providers.

Presets supply editable creation defaults, not vendor guarantees or discovery.
Existing routes do not change when presets change. Inference presets include
API-key services, account integrations for ChatGPT, Grok and Copilot, Bedrock
signing, Ollama and configurable compatible servers. OpenCode Go
uses one consolidated route for Chat/Responses, Messages and Gemini; clients
retain its client/session headers. Bedrock requires the regional runtime matching
its account.

Use the route URL without duplicating the vendor version prefix. Client headers:

| Service | Client credential header |
| --- | --- |
| Most services, subscription accounts | `Authorization: Bearer <token>` |
| Anthropic API, OpenCode Go Messages | `x-api-key` |
| Gemini/Live, OpenCode Go Gemini | `x-goog-api-key` |
| ElevenLabs | `xi-api-key` |
| Flux | `x-key` |

Query-key auth is unsupported. Claude, personal Antigravity and Grok CLI gateway
paths have fixture evidence; Cursor and Vibe remain gated. Version/session headers come
from clients. Advanced route fields enable `websocket: true` and `usage: auto`.
WebSockets require HTTP/1.1 and have a one-hour cap; revocation closes live use.
For Responses WebSockets, **Requests** gets one row per observed response turn,
usually as soon as the upstream terminal event arrives. A reused connection
can therefore show several rows before it closes. The first saved turn replaces
the connection's handshake row so it does not double-count usage. Turns still
open when the connection closes are recorded as incomplete, with unknown
usage if there were no terminal counters. A connection with no identifiable turn
keeps its connection row. Turn details link to the shared connection capture
when debug recording was enabled. Previously recorded connection totals are
not split retroactively.

Venice routes using the official `https://api.venice.ai/api/v1` root enable
response token accounting by default, including older routes where `usage` was
unset. An explicit `usage: off` still disables it. The term-llm Venice client
requests streaming usage automatically. Other streaming clients must send
`"stream_options": {"include_usage": true}`; the proxy does not rewrite their
request bodies or invent missing counts. Existing unmeasured records cannot be
backfilled from responses that were not retained.

Mistral routes using `https://api.mistral.ai/v1` also enable token observation
when `usage` is unset. Completed chat requests can show a USD estimate from
models.dev when the response reports usage and its model has a matching rate.
`usage: off` still disables observation, and this does not reprice old records.

### OpenCode Go: one key, one provider, one approval

Choose the **OpenCode Go** preset and store one upstream OpenCode Go API key.
The parent route prefix is `/opencode-go/`; approve the OpenCode Go provider once
to cover that route.
All protocols use the same scoped client token, not the upstream key. Configure
term-llm's OpenCode Go native base URL as `https://proxy.example.test/opencode-go`.
Its protocol clients select these three API roots:

| Protocol | Client API root | Fixed upstream API root |
| --- | --- | --- |
| Chat Completions / Responses | `https://proxy.example.test/opencode-go` | `https://opencode.ai/zen/go/v1` |
| Messages | `https://proxy.example.test/opencode-go/messages` | `https://opencode.ai/zen/go` |
| Gemini | `https://proxy.example.test/opencode-go/gemini` | `https://opencode.ai/inference/go/v1` |

The parent rule `/` handles Chat/Responses; `/messages/` and `/gemini/` select
Messages and Gemini respectively. Native clients append their operation paths:
`/chat/completions` or `/responses`, `/v1/messages`, and
`/models/<model>:streamGenerateContent`. Each protocol uses the client credential
header shown above. No body conversion is involved; the OpenCode client/session
headers still come from the client.

Named term-llm providers of type `opencode-go` use their own parent namespace:
for example, `household` uses `/household`, `/household/messages` and
`/household/gemini`. Create and approve that provider explicitly; it does not
borrow the built-in OpenCode Go route or another named provider's subpaths.

**Existing sibling-route installations require explicit migration.** Older
`/opencode-go-messages/` and `/opencode-go-gemini/` routes are not silently moved,
merged or approved by changing presets. Review and explicitly replace the old
setup with the consolidated parent and its three rules, then reapprove the
provider and update client roots; remove obsolete sibling routes/grants.
Clients approved before the migration may cover only some of the provider's
routes, so the provider shows as needing approval until you approve it again.
Editing an existing route's upstream, credentials or rules in the browser keeps
its client approvals; a file-managed reload still drops grants whose route
changed. Global term-llm proxy configuration now registers only the nested roots, so migrate the server and
reapprove before switching those clients. Explicitly manually registered legacy
sibling roots remain compatible when bound to the same client principal; missing
protocol roots fail closed rather than contacting a public vendor endpoint.

### Classification presets

Under **Providers → Add provider → Classification**, select **TypeSafe** (Jev),
**OpenAI Decisions**, or **Cloudflare Clef**. Store the vendor key on
that provider and approve it for the clients that need it. OpenAI Decisions is
approved independently of OpenAI chat and ChatGPT plans, even if you choose to
reuse the OpenAI API key. If your OpenAI key requires organization or project
headers, configure them on the proxy route, not the client.

| Proxy provider ID | `term-llm classify -p` | Default scoped requests |
| --- | --- | --- |
| `typesafe` | `typesafe` | `POST /v1/systemone`, `GET /v1/models` |
| `openai-decisions` | `openai` | `POST /decisions` (upstream base includes `/v1`) |
| `cloudflare-clef` | `cloudflare` | `POST /@cf/cloudflare/clef`, `POST /@cf/cloudflare/clef-flash` |

New TypeSafe routes default to **Pay as you go** and **Automatic** usage
measurement. On an existing route to `https://api.typesafe.ai`, change those
settings in place if they still show **Not specified** and **Off**. The route's
key and client approvals stay the same. The proxy estimates new System One
requests from the response's model and token counts. Its bundled
[TypeSafe list rate](https://docs.typesafe.ai/models) for `jev-1.13.0` and
`jev-1.14.0` is US$0.042 per million input tokens, with free output (verified
2026-11-22). TypeSafe's model API does not publish prices. If the rate changes,
use a Home price card for the route name and exact model ID (empty tier) to
override it for future requests. The proxy does not scrape the docs or reprice
past requests.

For Clef, enter the full upstream Workers AI run root
(`https://api.cloudflare.com/client/v4/accounts/<account-id>/ai/run`), or an AI
Gateway Workers AI root ending in `/workers-ai`. Fill in the IDs on the proxy;
leave off the model path. The bearer token stored on the proxy must be accepted
by the chosen endpoint.

For a new client configuration, set the proxy URL and token and omit
`proxy.providers` to discover approved classifiers. If you already have a
`proxy.providers` allowlist, keep it and add the proxy provider ID from the
table above. The IDs bind built-in classify selectors, not chat providers or
custom classifier aliases. To keep a classifier direct in automatic mode,
list its proxy ID in `proxy.direct` (for example, `openai-decisions`, not
`openai`). Local model, timeout, and image-format settings still apply.

To choose a default centrally, set **Client defaults → Classification** (or
`defaults.classify` in the proxy YAML). This is opt-in and doesn't change the
TypeSafe default just because another classifier is approved. See
[Client defaults](#client-defaults) for precedence.

While a classifier is bound to the proxy, it uses the scoped token instead of
local keys or headers, and `--base-url` cannot override its endpoint. Revocation
blocks access on the proxy immediately. After restart, automatic discovery won't
bind the revoked route, so local credentials may be used if configured. An
explicitly listed classifier with a missing or revoked grant fails closed even
after restart. If you're upgrading a client in automatic mode and want to keep
an already-approved classifier direct, add its proxy ID to `proxy.direct`.

Smoke-test TypeSafe with `term-llm classify models -p typesafe` and a classification
request using synthetic input. OpenAI Decisions and Clef list models locally,
so test them with an actual request, not `classify models`.

For a manual binding or a custom classification alias, set its `base_url` to the
local route URL (do not append `/v1` or the model path), and its `api_key` to a
reference to the scoped client token. Existing manual routes and grants are not
rewritten by adding a preset.

### Search and fetch presets

Add one provider and approve its current routes for the client. Exa and Jina each
have two routes that use the same named secret. Jina is listed under
**Embeddings** even though its Reader route serves page fetch. If you add
`jina-reader` to an existing Jina provider, approve it for clients that need it;
existing embedding grants do not cover Reader.

| Provider | Route profile | Upstream | Credential header | Client slot and selector | Preset `allow` |
| --- | --- | --- | --- | --- | --- |
| `exa` | `exa` | `https://api.exa.ai` | `x-api-key` | search: `exa` | `POST /search` |
| `exa` | `exa-mcp` | `https://mcp.exa.ai` | `x-api-key` | search, fetch: `exa_mcp` | `POST /mcp`, `GET /mcp`, `DELETE /mcp` |
| `brave` | `brave` | `https://api.search.brave.com/res/v1` | `X-Subscription-Token` | search: `brave` | `GET /web/search` |
| `tavily` | `tavily` | `https://api.tavily.com` | `Authorization: Bearer` | search: `tavily` | `POST /search` |
| `perplexity` | `perplexity` | `https://api.perplexity.ai` | `Authorization: Bearer` | search: `perplexity` | `POST /search` |
| `parallel` | `parallel` | `https://api.parallel.ai/v1` | `x-api-key` | search: `parallel` | `POST /search` |
| `youcom` | `youcom` | `https://ydc-index.io/v1` | `X-API-Key` | search: `youcom` | `POST /search` |
| `keenable` | `keenable` | `https://api.keenable.ai/v1` | `X-API-Key` | search: `keenable` | `POST /search` |
| `sofya` | `sofya` | `https://sofya.co/v1` | `Authorization: Bearer` | search: `sofya` | `POST =/search` |
| `jina` | `jina` | `https://api.jina.ai/v1` | `Authorization: Bearer` | embed: `jina` | Unrestricted by `allow` |
| `jina` | `jina-reader` | `https://r.jina.ai` | `Authorization: Bearer` | fetch: `jina` | `GET /*` |

For manual clients, use the scoped proxy token in the listed credential header;
the proxy substitutes the vendor key. Term-llm fills in these settings from
discovery, including the `/mcp` suffix on the Exa MCP endpoint. An Exa MCP grant
covers the endpoint, not individual MCP tools. DuckDuckGo is keyless and has no
proxy preset. Legacy Google Custom Search remains direct even in proxy-all mode:
its API key is sent in the request URL rather than an authentication header.

## Trusted accounts

Under **Providers**, select a provider and choose **Connection → Edit → Advanced**;
manual credential JSON is still supported for
`chatgpt`, `grok`, `copilot` and `aws-sigv4`. Use the guided sign-in for new
ChatGPT and Grok plan routes.
Select the account when creating its route. Credentials are write-only; metadata
contains state, mode and expiry. Account-managed headers cannot also have static
substitutions. Origins and API paths are restricted to that integration. Sign-out
clears credentials and closes streams; requests fail closed until credentials are
replaced. Claude Code and personal Antigravity use **Access → Add device**
and **Settings → Delete provider**, not this account editor.
For either shared subscription, use **Sign in to shared subscription** or
**Reconnect** to manage the operator sign-in.

For rotating accounts, move custody to the proxy: do not leave another process
refreshing the same token. Rotation is durably fenced and committed before use.
Uncertain post-connection issuer outcomes can require fresh login rather than
reusing a consumed token. Safe pre-connection failures back off. Inference is never
replayed automatically after a vendor 401. AWS supports static/session signing,
not automatic credential-chain renewal.

Operator-only startup import:

```yaml
accounts:
  home-chat:
    kind: chatgpt
    file: /run/secrets/chatgpt-account.json
```

The private regular file is limited to 128 KiB, read only when that account is absent.
After import the stored account is authoritative; remove the bootstrap file.
A different existing kind rejects startup. The Admin UI cannot introduce files.

## Using term-llm as a client

Use a top-level `proxy:` block in the client's `config.yaml`. Set `url` to the
proxy root, not a provider route URL, and use the scoped client token for the
routes you want to use:

```yaml
proxy:
  url: https://proxy.example.test
  token: file:///run/secrets/proxy-client-token
  tag: laptop
```

- `proxy.tag` is an optional label for this client, useful when clients share a
  token. Tagged requests appear as `<client> · <tag>` in the proxy's **By client**
  chart and **Requests** list. Use 1–64 letters, digits, dots (`.`), underscores
  (`_`), or hyphens (`-`), starting with a letter or digit. A tag requires a normal
  `sk-tlp-` client token.
- Omit `proxy.providers` to discover all providers advertised for the client token,
  including granted classifiers. No grant means automatic discovery does not bind
  that classifier. The token still limits which routes the client can use. In this
  mode, some unapproved integrations are unavailable instead of staying direct,
  including local providers such as Ollama. If you already use an allowlist,
  keep it and add new provider IDs. Use `proxy.direct` for exceptions in automatic mode.
  Restart the client to pick up changes to its approved providers.
- When set, `proxy.providers` is an allowlist. A provider name selects its granted routes,
  including companions: `exa` covers Exa REST and MCP; `chatgpt` covers its chat,
  usage, dictation, embeddings and realtime routes. To use only ChatGPT, set
  `providers: [chatgpt]`. For media or embeddings, list the provider name, such
  as `flux` or `jina`. A named inference provider such as `chatgpt-work` does
  not bind global feature settings such as `image.chatgpt.*`.
- Unlisted integrations stay direct. `providers: []` selects nothing in the
  top-level block. With `providers` omitted (**proxy-all** mode), use
  `proxy.direct` to keep named integrations direct. A nonempty `proxy.direct`
  conflicts with any explicit `proxy.providers` list, even an empty one.
- Parent names in `proxy.providers` and `proxy.direct` also cover their built-in
  companions when routes are missing. For example, if you list `exa` and only
  `exa-mcp` is granted, Exa REST stays unavailable instead of going direct.

Selected integrations use the advertised route and scoped proxy token instead
of local vendor keys or custom API roots. Missing routes that must stay proxied
use `/_proxy/client/unavailable/<id>`, which always returns `403` without
forwarding. In proxy-all mode, keyed search, media and embeddings integrations
stay bound even when their routes are missing. The exceptions are backends that
can run without a key: Exa MCP, Jina Reader, You.com and Keenable stay direct
and keyless if not advertised. For Exa MCP, You.com and Keenable, this suppresses
both configured keys and the environment fallback. To keep an integration direct
with your own key, opt it out (for example, `proxy.direct: [exa]`).

In proxy-all mode, `direct: [opencode-go]` keeps the whole OpenCode Go protocol
family direct, including named providers of that type. A named provider's own
exception affects only that provider and all its protocols. OpenCode Go uses
one parent approval with nested `/messages` and `/gemini` API roots. Complete
custom chat `url` mappings fail closed: use a compatible `base_url` or leave
that provider direct.

The canonical Claude route is `/claude-bin/`. With a top-level binding, term-llm
uses the granted route URL from discovery. If your launcher uses a different
token or route URL, use that exact URL and token in `providers.claude-bin.proxy`
instead. If you also have a top-level `proxy` block, give it an explicit
`providers` list that leaves out `claude-bin`. Run
`term-llm proxy run claude` without flags to use the binding already in your config.

### Client defaults

The proxy can set one default **route** per slot: `search`, `fetch`, `image`,
`audio`, `music`, `transcription` or `classify`. Use **Client defaults**, or set
`defaults` in the proxy YAML, for example `defaults: {search: exa-mcp, fetch: jina-reader, classify: typesafe}`.
The route must belong to a canonically named provider and its profile must serve
that slot. Naming a route distinguishes Exa REST from Exa MCP.

An explicit client selector such as `search.provider` or `search.fetch_provider`
wins. Classification follows the same precedence: command-line `-p`, then local
`classify.default_provider`, then an applicable proxy `defaults.classify` route,
then the existing TypeSafe default. The proxy default applies only if the client
is approved for and binds that classifier. Guardian, live voice, and grep
reranking also use the classifier default when their own
[provider settings](/guides/classify/#through-the-credential-proxy) are unset.
The other operator defaults apply only
if the client is granted and binds those routes. Without an applicable proxy
default, search and fetch use the sole bound selector if there is one; otherwise
they keep the built-in selectors (`exa_mcp` and `jina`, respectively). For example,
with both Exa routes bound, search defaults to Exa MCP; with only Brave bound, it
selects Brave. Media slots keep their built-in selectors unless there is an
applicable operator default. An unavailable route or a direct fallback does not
count as bound for this choice. There is no proxy default for `embed.provider`,
and inference providers are selected by name, not by a slot default.

### Client discovery

Term-llm loads granted, enabled routes and applicable default routes from
`GET /_proxy/client/v2/providers` using the client token. The response groups
routes under provider IDs and includes each route's local prefix and profile.
The proxy only advertises routes whose grant still matches their authority.
Discovery is cached for the life of the client process; restart after changing
routes, grants, defaults or the token to pick up the new configuration.
Revocation itself takes effect on the proxy without a client restart.

Discovery clients that send `X-Term-LLM-Proxy-Capabilities: classification`
can also receive the `classification` family. Without that header, v2 omits
classification providers so older clients can still discover their other routes.
To receive `defaults.classify` as well, a discovery client must send both
`classification` and `classification-default`. Older clients that only send
`classification` can still use explicitly listed classifier routes, but won't
receive the Classification default. Neither capability grants route access.

Discovery v1 (`/_proxy/client/v1/providers`) returns `410 Gone`. If v2 returns a
status other than `200 OK`, the client checks v1: a v1 `200 OK` identifies an
older proxy and prompts you to upgrade it. A discovery failure stops
configuration loading; it never switches the client to direct access.

## DNS SRV discovery

Use a `srv://` URL in client configuration to discover the proxy’s HTTPS host
and port through DNS. For example, set `proxy.url` to
`srv://_term-llm-proxy._tcp.example.test`. If the proxy is mounted under `/team`,
use `srv://_term-llm-proxy._tcp.example.test/team`; term-llm appends each
integration’s route beneath that prefix.

For `providers.<name>.proxy.url` or `term-llm proxy run claude` / `agy` with
`--url`, include the full scoped route path, for example
`srv://_term-llm-proxy._tcp.example.test/team/claude-bin`.

Use a DNS zone you trust. Term-llm connects over HTTPS to the first result in
the resolver’s priority/weight order, and the certificate must be valid for
that target hostname. If discovery fails, the connection fails without falling
back to a vendor endpoint. The server’s `--public-url` still takes a concrete
browser origin, not an SRV URL.

## Usage and spending

The **Usage** tab shows limits reported by your configured providers. It reads
subscription usage from Antigravity, Claude,
ChatGPT, Grok, and OpenCode Go, and can show an OpenRouter key's spending limit.
It uses the proxy's configured account or key, not a local CLI login. Each
reported window gets its own row, grouped by provider and model group. For
example, Antigravity's Gemini and Claude/GPT groups
can have separate short and weekly limits. A window's duration comes from the
provider; it need not be five hours.

The bar shows quota **used**; the tick shows **even pace** (the fraction of the
window elapsed when usage was sampled). If you have used 75% with only 60% of
the week elapsed, the forecast says **Runs out 1d 10h early**. Forecasts assume
your average usage rate continues. A missing forecast is not a promise that the
quota will last. See **Resets in** for the reset countdown.

The proxy refreshes these limits in the background every **15 minutes**, even
with the tab closed. Change the interval to **10–1,440 minutes** or refresh
manually. These checks don't invoke a model. If a provider has several bound
accounts or keys, each is refreshed separately: a failure marks only that
binding's last reading stale. The proxy may refresh an expiring OAuth token
even when no inference is running. A quota endpoint denying access does not
invalidate the token used for inference.

Unlike **Usage**, **Home** measures activity that passed through this proxy,
not the upstream account's allowance.

Home opens on **30 days of cost by model**. Choose **Cost** or **Tokens**, a range
(**Today**, **7d**, **30d**), and a split (total, client, provider, or model).
The breakdown lists the biggest contributors first, ranked by cost in the selected
currency or by token count, not by request count. Expand **more** to see the rest
by name. Every breakdown row is clickable: select a **client** or **provider** to
see its models, select a **model** to isolate its usage, or select **Total** to
switch to the model breakdown. The **Viewing** breadcrumb shows the active
filters. Use **×** to remove the last filter, or choose an earlier breadcrumb to
go back. Changing the metric, time range, or split keeps the active filters.

**Tokens** also splits usage into input, output, cache reads, and cache writes
for the range and each group. Hover, tap, or focus a bar to see the top 20
contributors for that hour or day. Expand the list for the rest; the total includes
them all. The chart uses your browser timezone: Today starts at local midnight,
and longer ranges use local calendar days. Currencies remain separate.

The address bar reflects the metric, range, split, and currency. Reload or
bookmark that URL to restore those preferences; opening bare `/_proxy/` restores
this browser’s last selection. Drill-down filters apply only while the Home view
is open; client, provider, and model identities are not saved in the URL or in
browser storage. **Reset view** clears the drill-down and returns to the default.

Only traffic through this proxy is counted. Missing evidence is not zero.
Provider token semantics are normalized without counting caches/reasoning twice;
legacy token history is recoverable only while original evidence remains.
The chart timezone does not change plan summaries or monthly alerts. Set their
timezone and the optional monthly alert under **Money settings**. Alerts warn;
they never stop requests.

Pricing uses the shared **models.dev** catalog, with exact provider/model matches
and published context tiers. Explicit operator rate cards override the catalog;
bundled first-party prices are an offline fallback. The proxy loads its snapshot
from `<state-dir>/pricing`, refreshes in the background (12-hour catalog TTL), and
keeps the last good snapshot if refresh fails. Request handling never fetches
prices. `TERM_LLM_OFFLINE` disables background refresh. Price details show the
source and retrieval date. Missing rates/counts stay unknown; no fuzzy matching,
automatic FX or invented invoice charges.

For account-backed Grok plans, a standard versioned Build response such as
`grok-4.7-build` can use the matching public API model (`xai/grok-4.7`) for its
API-equivalent estimate. Fast variants require their own published rates; they
never inherit standard pricing. The estimate preserves the reported model and
does not change plan billing or reprice historical requests.

For Venice chat completions, the proxy reads top-level `cost` or `usage.cost`
from the response, including the final usage chunk of a stream. It adds `usd`
and `diem` at **1 DIEM = US$1**, ignores `diam`, and rounds the sum once to the
nearest millionth of a dollar.
For example, `{"usd":0,"diem":0.0002498}` records **US$0.000250**. This is a
provider-reported cost, not a token-price estimate. Raw payload capture is not
required, and previously recorded requests are not repriced.

For routes using `https://api.exa.ai`, observation is enabled automatically
when `usage` is unset, including on existing routes. No new key, client grant,
or price entry is needed. On pay-as-you-go routes, the proxy records
`costDollars.total` from completed `POST /search` JSON responses as a USD
estimate, not a confirmed charge. Missing or invalid amounts, streamed search
responses, and previously recorded requests aren’t priced by this integration.
Set `usage: off` to disable observation.

Subscription activity is valued at the API rate when a stored **API-equivalent
estimate** is available. The Home Cost graph includes these estimates and shows
the plan portion in each bar's breakdown. They do not count as actual spending
or budget usage. Cache writes use the catalog's published default rate;
TTL-specific prices may differ, as noted in Price details. Unsupported service
tiers remain unpriced. Expand **Plan estimates at API rates** on Home for today
and month-to-date totals, coverage, and **Price details**.
Historic estimates keep their original price; old unpriced history is not
automatically repriced. Subscription fees and savings are not inferred; estimates
exclude taxes, discounts, credits and fees.

The request ledger shows model, client, normalized total input/output, cache-read
share, duration and price. If no model was reported, the row shows the method and
path. If token usage is unavailable, it shows bytes in and out when recorded.
Byte sizes are abbreviated; hover for exact counts. Dates and times use the
browser timezone. Expand a row for reported token counters and technical details.
Missing values are not zero. Total input includes
cached tokens exactly once across provider conventions. Repeated unmeasured HTTP
errors collapse within each page; expand ×N to inspect individual requests.
Measurement requires `usage: auto` and supported response evidence; arbitrary
protocols and responses without usage cannot supply exact counts.

In **Requests**, edit the fields under **Filters**, then choose **Apply filters**.
The count beside **Filters** describes the results on screen, not pending edits.
**Refresh** reloads the current page with the applied filters; **Clear** returns
to the first page of all requests. Visible Requests page 1 checks for changes
once a second using the applied filters. Unchanged results stay in place, and
changes wait while you inspect a request. Older pages and hidden tabs do not poll.
JSON export uses the applied query and includes
up to 1,000 matching metadata records, never bodies, query strings, arbitrary
headers or secret values. Admission commits before dispatch; incomplete outcomes
remain unknown. Requests default to 30-day retention; financial history is separate.
Path redaction cannot detect arbitrary secrets: avoid sensitive path identifiers.

## Configuration

A secret is a name and a value, not bound to an upstream origin. Routes can
reference it by name, including from different hosts. Referenced secrets cannot
be deleted. Values are write-only in the admin UI, but anyone who can edit routes
can send any available key to an upstream they choose. Restrict route editing to
trusted administrators. Client grants still pin the upstream origin and base
path and the secret name; clients cannot redirect the key.

YAML import/export contains definitions and references, not stored secret values,
client tokens, passkeys or history. Only operator-authored files may introduce
private regular secret files (maximum 16 KiB, trailing newlines removed).

With `--config ./proxy.yaml`, routes are read-only; explicit reload validates and
activates atomically, retaining the old configuration on failure. Files are not
watched. Example:

```yaml
providers:
  service:
    label: Example Service
    client: {type: openai_compatible}
routes:
  - name: service
    provider: service
    local_prefix: /service/
    upstream: https://api.example.com/v1
    caller_auth: {header: Authorization, prefix: "Bearer "}
    set_headers:
      Authorization: {secret: service-key, prefix: "Bearer "}
    usage: auto
    billing: pay_as_you_go
    redirects: refuse
secrets:
  service-key:
    file: /run/secrets/service-key
```

Every route needs a `provider` matching an entry under `providers`. Include
`profile` for preset routes; a hand-made route can omit it and use `client.type`
for inference. A profiled provider's family comes from its profile; an
unprofiled provider defaults to `inference`.

Enabled defaults true. `stored: <name>` references an existing same-name secret.
HTTP upstreams need explicit `allow_insecure_http: true`. Prefixes must be canonical,
non-overlapping, non-root and end with `/`; admin/asset/health paths are reserved.
There is no interpolation, command execution or body rewriting.

Without `allow`, routes can grant all methods and relative paths under their
upstream base, potentially including management APIs. Use narrow credentials.
Plain entries such as `POST /responses` match by literal path prefix, not by
segment or exact path. Use `POST =/responses` to match only the exact path
relative to the upstream base. Account routes always enforce integration
restrictions.
`redirects` is `passthrough` or `refuse`; WebSockets/usage default off. Billing defaults
unspecified; choices are `pay_as_you_go`, `plan`, `local`, `unspecified`.

Changing upstream authority/base, credential rules, account or access policy removes
existing grants for the affected routes, so clients need provider approval again.
Delete/recreate never restores grants or approvals. Disabling retains grants;
rotating a secret in place retains them. A replacement credential may have
broader vendor privileges: the proxy cannot infer those.

Provider approval is not part of route YAML. It is stored per client and expands
into route grants for the routes that existed when the approval was made. A
configuration reload or import that changes route authority can therefore leave
a client marked as needing approval.

### Catch-all `allow` entries

Use `METHOD /prefix/*` when the path tail is opaque, for example when it contains
an escaped identifier or a URL. The method must be explicit and uppercase
(`CONNECT` is not allowed); `*` is allowed only as the entire final path segment.
`GET /v1/*` matches `/v1/` and paths beneath it, not `/v1`, `/v10/x` or an encoded
slash in place of the literal boundary. `GET /*` covers any safe path for `GET`
only.

Matching uses the escaped path relative to the selected destination. The prefix
must be canonical. The tail can contain doubled slashes, colons and escapes,
but the path must be at most 8 KiB and cannot contain dot segments, backslashes
or control characters, including after decoding. Allowed paths and queries are
forwarded without normalizing the tail. On a subpath route, put `allow` on the
individual rule. Catch-alls are not allowed on account routes. Adding or widening
one requires new approval for that route.

### DNS SRV upstreams

A route’s `upstream` (or any subpath rule’s `upstream`) can be a DNS SRV URL,
for example `srv://_inference._tcp.example.com/v1`. The server selects the first
result in the resolver’s priority/weight order and connects to that host and
port over HTTPS, preserving the base path. Use a DNS zone you trust and a TLS
certificate valid for the discovered hostname. For example, if the record
points to `api.example.com` on port `8443`:

```yaml
providers:
  inference:
    label: Inference gateway
    client: {type: openai_compatible}
routes:
  - name: inference
    provider: inference
    local_prefix: /inference/
    upstream: srv://_inference._tcp.example.com/v1
    set_headers:
      Authorization: {secret: inference-key, prefix: "Bearer "}
secrets:
  inference-key:
    file: /run/secrets/inference-key
```

Discovery runs on startup, when saving routes, and on operator file **Reload**,
not on each request or when rotating a secret. If discovery fails, the new
configuration is rejected and any active routes stay unchanged. The upstream
portion of a client approval uses the exact configured `srv://` string, not
the selected host or port. This also applies to trusted-account routes, which
still enforce the account's permitted upstreams. Use SRV only where you trust
every target published for that record with the route's credential. DNS changes
alone do not move active traffic.

Existing approvals tied to the SRV record carry forward automatically. Only
older approvals tied to a resolved host and port need to be reapproved in
**Clients**. Client tokens do not need to change; static HTTP(S) grants are unaffected.

## Explicit setup and production administration

Passkey administration is default. For controlled enrollment:

```sh
install -d -m 0700 ./proxy-state ./proxy-secrets
(umask 077; openssl rand -hex 32 > ./proxy-secrets/bootstrap)
term-llm serve proxy --state-dir ./proxy-state \
  --public-url http://localhost:8082 \
  --passkey-bootstrap-token-file ./proxy-secrets/bootstrap
```

A bootstrap/recovery secret exchanges once within ten minutes of startup for a
five-minute enrollment grant. Remove explicit flags/files after enrollment;
automatic local bootstrap files are cleaned up. Enrollment is not a machine token
or permanent admin password. Do not reuse secrets on restart.

### Passkeys, sessions and recovery

Keep a spare passkey. Adding/revoking keys or signing out other browsers needs fresh
authentication; the final key cannot be removed. Sessions expire after 12 idle hours
or seven days overall and survive restart via private token hashes. Restart clears
pending ceremonies/grants, not keys or clients. Logout revokes its session.

If keys are lost, restart the same store with a fresh private
`--passkey-recovery-token-file`, choose **Recover access**, enroll and sign in.
Recovery invalidates sessions but retains old keys: explicitly revoke lost keys,
then remove the recovery flag/file. Routes and upstream credentials are unchanged.

### Explicit token compatibility mode

Legacy/local administration may use `--auth token --admin-token-file <private-file>`.
Rotate by replacing the file and restarting; eight-hour sessions also end on restart.
Passkey mode has no admin-token bypass, and incompatible auth flags are rejected.

### HTTPS termination and hostname routing

The listener has no built-in TLS. Remote deployments require a stable domain,
`--public-url https://proxy.example.test`, explicit enrollment and a trusted external
terminator. Route the whole hostname at its root; preserve Host and paths including
`/_proxy/`, client routes and `/healthz`. Mount paths/IP passkey origins are unsupported.
Keep the backend private; do not trust arbitrary forwarded headers.

```caddyfile
proxy.example.test {
    reverse_proxy 127.0.0.1:8082 {
        flush_interval -1
    }
}
```

Use real DNS/trusted certificates; `.test` is illustrative. Never disable TLS
verification. Preserve streaming/cancellation, allow request timeouts, and do not
add retries. Secure/HttpOnly/SameSite cookies, exact Origin/Host and CSRF protect
administration; network/rate limits still matter. Protect machine traffic with TLS.

## Forwarding behavior

Headers replace rather than append. Duplicate credentials are rejected. Client
auth, cookies, forwarding/rewrite/proxy and hop-by-hop headers are stripped;
upstream Host is fixed and Set-Cookie removed. Opaque bodies, escaped identifiers
and raw queries pass through. Usage-enabled routes request identity encoding;
unexpected encodings/extensions remain unmeasured. Redirects are never followed.
Traversal, controls, absolute targets, CONNECT and unsupported upgrades are refused
by ordinary forwarding. The constrained Claude Code and personal Antigravity
tunnels above are separate CONNECT paths that intercept TLS only for their fixed
provider hosts and require a matching route grant. This is not a general forward
proxy or TLS interceptor. A trusted upstream can echo
its own credential. Credential custody also requires separate worker
filesystems/identities and egress policy: routing settings alone are not an OS
sandbox.

## Storage and operations

State defaults to the user config directory's `term-llm/proxy`; override with
`--state-dir`. Run one process per store. `proxy.db` holds plaintext credentials,
client hashes/grants and history; `admin-auth/` holds passkeys/sessions. Native
Claude and Antigravity shared sign-ins use authenticated ciphertext keyed to
proxy-owned material, but the database is not a general encrypted vault. Use
0700 directories and 0600 files and owner-only Windows ACLs. No credentials are stored in browser storage. Protect
backups like credentials; use consistent SQLite backup or stop the process, not a
live DB-only copy. Include `claude-forward-ca-v2.pem` and `agy-forward-ca-v2.pem`
for the native integrations you use. Each private key is also needed to decrypt
that provider’s stored sign-in. Losing one requires a new operator sign-in for
that provider.
General ledger writes use WAL/NORMAL; credential custody uses FULL-synchronous
commits before returning tokens or dispatching refreshes.

Schema v5 replaced browser pairing with the native Claude vault. Schema v6 adds
admin API key hashes and provisioning metadata; existing clients stay browser-managed. The v5 binary rejects v6 for read-write use: stop
older writers before upgrading, and restore a protected backup to downgrade.
The proxy allows up to 1,024 active clients and keeps up to 4,096 client rows;
new clients can evict the oldest revoked rows without removing history. In the
admin UI, open **Clients → Revoked** to permanently delete one client or select
**Delete all revoked**. The **Requests** page still shows their past activity,
and spending history is kept.

```sh
term-llm serve proxy health --host 127.0.0.1 --port 8082
```

Health checks local readiness, not vendors. General `--debug-raw` and profiling
are refused; payload capture has a separate explicit opt-in below.
Defaults: 64 concurrent requests, 64 MiB body, 15-minute request deadline, 30-day request
history. CLI flags override these. Retention is batched; monitor disk. Shutdown
stops admission, drains for 20 seconds, then cancels remaining work.

### Upgrade to stored providers (schema v9)

Back up the proxy state and any operator YAML. Upgrade and restart the proxy
and term-llm clients together: discovery v1 has no compatibility alias.

- **Database mode:** v9 migrates existing routes into stored providers and drops
  secret origin bindings without changing key values. If the migration aborts,
  the proxy does not start and a v8 database stays at v8. Follow the reported
  remediation with the previous build, then retry, or restore the backup.
- **File mode:** convert the operator YAML before restarting. Add `provider` to
  every route, `profile` to preset routes, and a matching entry under `providers`
  with a `label` and any old client hints nested under `client`. Remove `origin:`
  from secret declarations. For Claude, change the old profile, canonical
  route and provider name, and account `kind: claude-code` to `claude-bin`;
  replace `/claude-code/` with `/claude-bin/` if using the old canonical prefix.
  Keep existing account names and noncanonical route names. YAML import also
  rejects legacy fields.
- **Legacy `allow` entries:** remove or rewrite any entry containing `*` on a
  route or subpath rule before upgrading. v9 refuses to reinterpret a legacy
  literal `*` as a wildcard. Add any intended catch-all after migration, and
  reapprove the affected routes.
- **Claude workers:** the canonical route moved from `/claude-code/` to
  `/claude-bin/`, with no alias. Existing grants for the migrated route move
  with it. Update any copied provider YAML or explicit route URLs. Workers must
  re-run the copied Claude launcher command with the new URL because native
  client state is keyed by the route root.
- **Proxy-all search:** when the proxy does not advertise Exa MCP, You.com or
  Keenable, the client no longer sends its own `EXA_API_KEY`, `YDC_API_KEY` or
  `KEENABLE_API_KEY` to those backends. They use the vendor's keyless limits.
  To keep direct, keyed access, add the provider to `proxy.direct`, for example
  `proxy.direct: [exa]`.

### Shorter built-in provider labels (schema v11)

On upgrade, the proxy shortens old built-in default labels in stored
configuration (for example, **Grok plan** becomes **Grok**). A label changes
only if it exactly matches the old default for a route profile the provider
owns. Custom labels, provider IDs, URLs, grants, and credentials stay as they
are. If you use operator YAML, you can update its `label` fields yourself;
the proxy does not rewrite the file. Back up your state before upgrading:
older binaries reject the newer schema marker.

### Debug payload capture

Capture is **off by default**. Open **Debug** in the signed-in proxy UI to enable
or disable it without restarting, and inspect captured request/response payloads.
Enabling requires confirmation that raw data may contain secrets. Disabling stops
capture, including active streams, without interrupting forwarding. Toggle changes
apply only until restart; startup follows the CLI options.

Without a directory flag, captures use `<state-dir>/debug-payloads`. To choose
another private archive and start with capture enabled, keep your other startup
options and add:

```sh
term-llm serve proxy --debug-payload-dir ./tmp/proxy-payloads
```

**Sensitive:** payloads contain unredacted prompts, replies, tool data and any
body-embedded secrets. A trusted upstream may echo credentials into its body.
Keep this directory out of source control, shared/synced folders and ordinary
backups. Capture uses private directories/files (0700/0600, owner-only Windows
ACLs), rejects symlinks and requires exclusive archive ownership. Do not enable
it routinely in production. Startup prints a warning; file I/O can affect latency.

Each dispatched request gets a directory named for its proxy request ID (also
returned in `X-Term-LLM-Proxy-Request-ID`), containing:

- `request.body` and `response.body`: binary-safe bytes consumed on the forwarding
  path; empty or unread streams may have no file.
- `request.headers`, `upstream-request.headers`, and `upstream-response.headers`:
  bounded header previews from the client, after the proxy applies upstream
  credentials, and from the upstream response. Compare the first two to see
  whether the proxy substituted a credential. Bearer tokens show at most their
  first three characters; short or malformed tokens, cookies, API keys, session
  IDs and other sensitive values stay redacted. A few diagnostic values, such as
  `Content-Type`, `User-Agent`, and `X-Opencode-Client`, are shown when available.
- For upgraded WebSockets, `websocket-client.frames` and
  `websocket-server.frames`: raw wire frames, **not decoded JSON messages**.
- `manifest.json`: bounded metadata, response format, reported model/usage when
  available, and per-stream bytes seen/stored, EOF, truncation and error flags.
  While active, files may grow and the manifest is preliminary. `finished` means
  capture finalization, **not** proof of a complete payload; inspect those flags.

No full credential values, cookies, or query strings are recorded in the header
previews; bodies are not redacted, decompressed, replayed or pre-read. Captures describe consumed
transport bytes, not proof that all upstream work reached the client. Capture
failures do not fail forwarding. Local pre-dispatch denials are not captured.
To inspect one request, expand its row in **Requests** and choose **View raw
payload** or **View header summaries**. The button appears when that request has
stored capture data, even if capture has since been turned off. Expanding the row checks availability;
it does not fetch payload bytes or turn capture on. Close the row to return to the
same filtered list. The signed-in **Debug** view also lists captures and shows
bounded previews, including manifests, HTTP bodies, and WebSocket frames. Frame
files open in **Decoded** view: client masking is removed, text and JSON are
readable, and frame headers show their wire offsets. Use **Raw bytes** to see
the original capture. Binary, compressed, and incomplete data are identified
rather than guessed at. Decoding is on demand; it does not change the archive.
Payloads are displayed as text, not HTML, and never saved in browser storage.
Only this fixed archive and allowlisted capture files can be inspected; this is
not a general log-file browser. Normal request exports still exclude bodies.
Debug inspection requires the same authenticated administrator session and
same-origin protections as other administration; capture changes require CSRF.

Defaults are **4 MiB per body/WebSocket direction**, **2,560 MiB (2.5 GiB) archive bytes** and
**1,000 request captures**. Override byte limits with
`--debug-payload-max-bytes` (1 byte–64 MiB) and
`--debug-payload-max-total-bytes` (32 KiB–4 GiB). The archive rolls: when a byte
or count limit would be exceeded, the proxy removes the oldest inactive captures
first, including captures kept across restarts. In-flight captures are never
removed. If there is still not enough room, capture can be skipped or truncated
without interrupting proxy traffic. The request ledger is unaffected. Disable
capture after debugging. Restart without `--debug-payload-dir` to leave startup
capture off.

### What is and is not verified

Synthetic upstreams, native-binary fixtures and client/browser fixtures cover scoped
routing, the launcher environment, CA handling, vault placeholders, token exchange
and refresh, cancellation, consent and charts. They do not prove live vendor
issuance, subscription acceptance, live native sign-in or billing. The
Antigravity fixtures use synthetic OAuth and Google endpoints, not a real
Google account. Cursor, Vibe and
exact custom-URL mappings remain gated. No invoice/refund, FX, plan-quota or hard financial-limit
support is claimed.

See the [deployment example](https://github.com/term-llm/term-llm/tree/main/examples/proxy)
for containers; state and upstream keys must stay off workers.
