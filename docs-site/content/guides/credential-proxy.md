---
title: "Credential proxy"
description: "Keep provider credentials on the proxy; connect clients with revocable tokens."
weight: 45
---

`term-llm serve proxy` forwards HTTP/SSE and opt-in WebSockets to fixed upstreams,
substituting server-held credentials. It runs independently of chat and provider
configuration. Setup does not probe vendors. Each client has its own route grants;
admin sessions never authorize forwarding.

## Quick start: local passkey administration

```sh
term-llm serve proxy
```

1. Open **http://localhost:8082** on the same computer. Use `localhost`, not an IP:
   passkeys bind to the hostname.
2. Run the printed `cat` command, paste the bootstrap token, name your passkey and
   enroll. The token expires ten minutes after startup; restart if it expires.
3. Choose **Connect Claude** below, or **Routes → Add route** for another provider.
   Select its preset and stored credential; saving a route does not grant access.
4. Under **Clients**, create a client with selected routes. Save its shown-once
   token. Set your application's API root to the local route URL and its credential
   to this client token, **not** the upstream key. Examples appear under Clients.

The listener defaults to `127.0.0.1:8082`. Keep its state directory across restarts.
Remote administration requires a stable HTTPS hostname and explicit enrollment.

## Connect standalone Claude Code

**Connect Claude** accepts one subscription OAuth token from the official
`claude setup-token` command—not an Anthropic API key. It creates the account,
narrow route, plan billing, usage measurement and scoped client grant atomically.
Token-only credentials need no refresh token; opaque expiry is unknown unless
provided by the issuer. Rejected or expired inference credentials require reconnect.

On each Claude computer:

```sh
term-llm proxy connect claude --url https://proxy.example.test
claude
```

Open the approval link, sign in and **type the code from your own terminal**.
Never approve a link/code someone sent you. Existing working accounts pair only
another scoped client, without issuing or replacing an upstream token. Eligible
legacy connections can be adopted through a narrow alias after consent; ambiguous
or wider routes require an explicit choice.

First setup/reconnect asks before running `claude setup-token`, privately capturing
its output while you complete sign-in. `--paste-token` provides masked input when
native capture is unavailable. Enrollment temporarily holds the upstream token on
that trusted computer; no live vendor issuance/acceptance is established by fixtures.

After approval, only `env.ANTHROPIC_BASE_URL` and `env.ANTHROPIC_AUTH_TOKEN` in the
selected user's `~/.claude/settings.json` are merged. The latter is a scoped proxy
credential. Private backup/undo preserves hooks, plugins, permissions and unrelated
settings; conflicting auth is refused. No shellrc, project trust or global term-llm
`proxy:` block changes. Restart existing Claude processes after changing identity.
Trusted project settings/hooks can still override user configuration.

- Omit `--url` to reuse the connector URL or be prompted; local default is
  `http://localhost:8082`. Remote connections require verified HTTPS.
- `--reconnect` explicitly replaces account credentials with token-only mode,
  retaining existing clients/grants. Replacing rotating credentials stops refresh.
- `--cancel` cancels a saved pending request; uncertain commits recover from the
  same receipt rather than minting another client.
- `term-llm proxy disconnect claude` restores owned gateway keys, preserving later
  unrelated edits. Earlier direct auth may resume; revoke unused clients separately.

Approval and result lifetimes are bounded. Failed handoff can leave a newly issued
vendor token unused: revoke it there. Shared reverse-proxy peers share pairing
capacity. Operator-file mode cannot create new connection routes/aliases.

## Provider presets and client settings

Presets supply editable creation defaults, not vendor guarantees or discovery.
Existing routes do not change when presets change. Available families include
API-key inference/media/search services, renewable ChatGPT/Grok/Copilot/Claude
accounts, Bedrock signing, Ollama and configurable compatible servers. OpenCode Go
has separate Chat/Responses, Messages and Gemini routes; clients retain its
client/session headers. Bedrock requires the regional runtime matching its account.

Use the route URL without duplicating the vendor version prefix. Client headers:

| Service | Client credential header |
| --- | --- |
| Most services, subscription accounts | `Authorization: Bearer <token>` |
| Anthropic API, OpenCode Go Messages | `x-api-key` |
| Gemini/Live, OpenCode Go Gemini | `x-goog-api-key` |
| ElevenLabs | `xi-api-key` |
| Flux | `x-key` |

Query-key auth is unsupported. Claude and Grok CLI gateway paths have fixture
proof; Cursor, Antigravity and Vibe remain gated. Version/session headers come
from clients. Advanced route fields enable `websocket: true` and `usage: auto`.
WebSockets require HTTP/1.1 and have a one-hour cap; revocation closes live use.

## Trusted accounts

Under **Routes → Accounts**, import credential JSON for `chatgpt`, `grok`,
`copilot`, `claude-code` or `aws-sigv4`; use Connect Claude for token-only setup.
Passkey mode requires fresh consent. Select the account when creating its route.
Credentials are write-only; public metadata contains state, mode and expiry.
Account-managed headers cannot also have static substitutions. Origins and API
paths are restricted to that integration. Sign-out clears credentials and closes
streams; the route fails closed until reconnected.

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
A different existing kind rejects startup. Browser imports cannot introduce files.

## Using term-llm as a client

In the client's `config.yaml`:

```yaml
proxy:
  url: https://proxy.example.test
  token: file:///run/secrets/proxy-client-token
  direct: []
```

Supported integrations use scoped proxy credentials instead of resolving local
provider secrets, including explicit custom roots. Built-ins use preset route IDs;
named compatible/vLLM providers use their configured names. Keep prefixes
`/<name>/` and grant every needed route. Missing routes/grants fail, never fall back.
Media and account integrations may require sibling routes such as
`chatgpt-backend`, `chatgpt-openai` or `opencode-go-messages`.

`direct` explicitly retains selected integrations' local credentials/endpoints.
Unknown names fail; OpenCode Go must select its whole protocol family. Complete
custom chat `url` mappings fail closed: migrate to a compatible `base_url` or
choose direct intentionally. Provider/doctor output is offline configuration,
not connectivity proof. The proxy token resolves at config load; prefer a private
file. Replacing it requires worker restart: one root retains one caller identity
per process. This block does not configure standalone Claude; use its connector.

## Usage and spending

Home charts **Cost/Tokens**, **Total/By client/By provider**, **Today/7/30 days**.
Hover, tap or use the keyboard for values. Today uses hourly local-time bins;
longer ranges use calendar days across months. Currencies remain separate.

Only traffic through this proxy is counted. Missing evidence is not zero.
Provider token semantics are normalized without counting caches/reasoning twice;
legacy token history is recoverable only while original evidence remains.
Prices/sources and limitations appear under Details. Budgets warn, never stop work.

Exact, dated offline first-party prices cover a limited verified model/tier subset;
unknown models, unsupported cache writes/tiers and custom/aggregator prices remain
unpriced. Exact dated manual cards override. Historic estimates keep their original
price. No fuzzy matching, automatic FX or invented invoice charges. Subscription
activity has no retail API-equivalent charge; allowances, fees and extra charges
are not inferred. Estimates exclude taxes, discounts, credits and fees.

Requests shows paginated metadata and expandable usage details. Filters/export
exclude bodies, queries, arbitrary headers and secret values. Export is capped
at 1,000 matching records. Admission commits before dispatch; incomplete outcomes
remain unknown. Requests default to 30-day retention; financial history is separate.
Path redaction cannot detect arbitrary secrets: avoid sensitive path identifiers.

## Configuration

UI-managed routes/secrets are editable. Secrets bind to one origin; referenced
secrets cannot be deleted. YAML import/export contains definitions/references,
not credentials, client tokens, passkeys or history. Only operator-authored files
may introduce private regular secret files (maximum 16 KiB, trailing newlines removed).

With `--config ./proxy.yaml`, routes are read-only; explicit reload validates and
activates atomically, retaining the old configuration on failure. Files are not
watched. Example:

```yaml
routes:
  - name: service
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
    origin: https://api.example.com
    file: /run/secrets/service-key
```

Enabled defaults true. `stored: <name>` references an existing same-name secret.
HTTP upstreams need explicit `allow_insecure_http: true`. Prefixes must be canonical,
non-overlapping, non-root and end with `/`; admin/asset/health paths are reserved.
There is no interpolation, command execution or body rewriting.

Routes otherwise grant all methods/relative paths beneath their base, potentially
including management APIs. Use narrow credentials and optional `allow` entries
such as `POST /responses`. Account routes always enforce integration restrictions.
`redirects` is `passthrough` or `refuse`; WebSockets/usage default off. Billing defaults
unspecified; choices are `pay_as_you_go`, `plan`, `local`, `unspecified`.

Changing upstream authority/base, credential rules, account or access policy removes
existing grants; review/regrant explicitly. Delete/recreate never restores grants.
Disabling retains grants; rotating a same-origin secret retains them. A replacement
credential may have broader vendor privileges: the proxy cannot infer those.

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
Traversal, controls, absolute targets, CONNECT and unsupported upgrades are refused.

This is not a general forward proxy or MITM. A trusted upstream can echo its own
credential. Credential custody also requires separate worker filesystems/identities
and egress policy: routing settings alone are not an OS sandbox.

## Storage and operations

State defaults to the user config directory's `term-llm/proxy`; override with
`--state-dir`. Run one process per store. `proxy.db` holds plaintext credentials,
client hashes/grants and history; `admin-auth/` holds passkeys/sessions. This is not
an encrypted vault. Use 0700 directories and 0600 files and owner-only Windows ACLs.
No credentials are stored in browser storage. Protect backups like credentials;
use consistent SQLite backup or stop the process, not a live DB-only copy.
WAL/NORMAL does not guarantee recent writes survive power loss. Downgrade from a
compatible protected backup.

```sh
term-llm serve proxy health --host 127.0.0.1 --port 8082
```

Health checks local readiness, not vendors. Raw debugging/profiling is refused.
Defaults: 64 concurrent requests, 64 MiB body, 15-minute request deadline, 30-day request
history. CLI flags override these. Retention is batched; monitor disk. Shutdown
stops admission, drains for 20 seconds, then cancels remaining work.

### What is and is not verified

Synthetic upstreams and actual clients/browser fixtures cover scoped routing,
renewal, cancellation, token-only onboarding, consent and charts. They do not prove
live vendor issuance, subscription acceptance, billing or non-Linux compatibility.
Cursor/Vibe/Antigravity and exact custom-URL mappings remain gated. Token-only
native issuance has contract tests, not a live account sign-in. No invoice/refund,
FX, plan-quota or hard financial-limit support is claimed.

See the [deployment example](https://github.com/term-llm/term-llm/tree/main/examples/proxy)
for containers; state and upstream keys must stay off workers.
