---
title: "Credential proxy"
description: "Keep upstream credentials server-side while local callers use individually granted tokens."
weight: 45
---

`term-llm serve proxy` is a **standalone, generic HTTP credential proxy**. Configure
an authenticated local path prefix, a fixed upstream base URL and header
substitutions. The proxy supplies real credentials; clients receive only revocable
local caller tokens. It does not start chat, load agent/provider configuration,
discover models or make automatic upstream test requests.

There are no provider presets, model policies, JSON/body inspectors, retries,
tunnels or native API assumptions. Two unrelated services with different base
paths and auth headers use exactly the same configuration mechanism.

## Start with passkey administration (default)

Provision a one-use host enrollment secret and an independent private state directory:

```sh
install -d -m 0700 ./proxy-state ./proxy-secrets
(umask 077; openssl rand -hex 32 > ./proxy-secrets/bootstrap)
term-llm serve proxy --state-dir ./proxy-state \
  --public-url http://localhost:8082 \
  --passkey-bootstrap-token-file ./proxy-secrets/bootstrap \
  --host 127.0.0.1 --port 8082
```

Visit **http://localhost:8082/_proxy/** (not the IP address). Choose **Set up a
passkey**, exchange the private file's contents and enroll a named passkey through
your browser. User verification is required. The host secret is not a permanent
admin password or a caller credential. Never put it in a URL, browser storage,
agent workspace or source control. The proxy never prints generated credentials.

The host secret is available for **10 minutes from startup** and can be exchanged
only once for a five-minute HttpOnly enrollment grant. An interrupted browser
ceremony can be retried while that grant remains valid. If it expires or the
grant cookie is lost, restart explicitly with a fresh private secret file. After
first enrollment, remove the bootstrap flag and securely remove its file; normal
startup with enrolled credentials requires neither bootstrap nor recovery files.
An old bootstrap flag cannot enable enrollment once a credential exists.

`--auth passkey` is the default and requires an exact `--public-url`. A stable
HTTPS **domain origin** is required for remote use; `http://localhost[:port]` is
only a development exception. IP origins and mount paths are rejected. Passkeys
are scoped to the hostname: choose it before enrollment. Credentials live in
`<state-dir>/admin-auth/credentials.json`, separately from agent/Hub state, guarded
by an exclusive process lock. The directory must be 0700 and credential/host
secret files private regular files (0600 or stricter). Bootstrap/recovery files
cannot be symlinks. On Windows, Go file modes do not verify ACLs: provision an
owner-only directory/profile ACL explicitly. Provision at least 256 random bits;
there is no default password. Keep state, WAL/SHM files and backups private.

Without `--config`, routes and write-only secrets are managed in the UI and
persisted in independent `proxy.db`, alongside caller hashes/grants and request
metadata. Committed changes affect **new** requests immediately; dispatched work
continues with its admitted configuration. Browser authentication, logout,
recovery and process restarts do not change machine caller tokens or grants.
Do not run two proxy processes against the same state directory: live
configuration is process-local.

### Explicit token compatibility mode

For a local/legacy deployment that intentionally does not use passkeys:

```sh
(umask 077; openssl rand -hex 32 > ./proxy-secrets/admin)
term-llm serve proxy --auth token --state-dir ./proxy-state \
  --admin-token-file ./proxy-secrets/admin --host 127.0.0.1 --port 8082
```

Visit `http://127.0.0.1:8082/_proxy/` and exchange the file's contents. **There is no
silent token fallback or admin-token bypass in passkey mode:** that mode rejects
`--admin-token-file`; token mode rejects passkey enrollment/recovery flags. Changing
modes requires an explicit operator restart and invalidates browser sessions.
Token-mode admin credentials cannot authorize forwarding. Rotate a token by
replacing its private file and restarting. Treat both modes' OS operator as trusted.

### Passkeys, sessions and recovery

The **Passkeys** view lists named credentials and last-use times. Add a spare
passkey before losing your primary one. Each additional enrollment, credential
revocation or **Sign out other browsers** action requires a fresh passkey assertion
and consumes a one-use recent-auth authorization (five-minute maximum). The final
credential cannot be deleted. Revoking a credential revokes every session issued
with it, including your current session when applicable; signing out other browsers
keeps the current browser. Already admitted requests can finish.

Passkey sessions use the shared authentication engine's **12-hour idle / seven-day
absolute** limits and bounded 1,024-session registry, but are intentionally
**in memory**: restart signs out all browsers and clears pending ceremonies/grants.
Logout immediately revokes the current server session, not merely its cookie.

If every enrolled device is unavailable, an OS operator can restart the same proxy
with `--passkey-recovery-token-file ./proxy-secrets/recovery` (a newly provisioned
private random secret). This is available only with an already enrolled store.
Choose **Recover access**, exchange the one-use secret, and enroll a replacement.
Recovery adds a key, invalidates **all** sessions and pending challenges, and then
requires an ordinary passkey login; it never creates an admin-token session.
Old keys remain enrolled so you can inspect and explicitly revoke them after login.
Remove the recovery flag/file after use. Recovery has the same 10-minute exchange /
five-minute grant limits as bootstrap. Reusing a file in an explicit restart rearms
its host grant; always use a fresh secret instead. No recovery endpoint changes
routes, machine caller tokens/grants or upstream credentials.

### A short setup journey

1. **Create a secret.** Give it a short reference name, the intended upstream
   origin and its value. Values can be entered/replaced but never read back.
   Creating a duplicate name fails; use **Replace** deliberately to rotate a key.
2. **Add a route.** Enter an immutable name, local prefix ending in `/`, upstream
   base URL and the local caller's auth header/prefix. Add structured header rules
   using secret references and optional literal prefixes, or nonsecret literals.
3. **Create a caller.** Name the client and explicitly select its permitted routes.
   Copy its generated token now: only a hash is stored and it is shown once.
   You can edit that caller's route grants later without changing its token.
4. Point the client at the local prefix and use that caller token in the configured
   local header. Use the generic curl example; no provider-specific client code is
   required. The client must support changing its destination URL/auth header.
5. Open **Requests** to inspect actual calls. No setup action sends a speculative,
   possibly billable request to the upstream.

A caller has the upstream credential's authority across **all ordinary methods and
relative paths under the chosen base**. This may include account, management or
billing APIs. Use narrowly scoped credentials and base paths. There is no
path/method policy language and no automatic direct-provider fallback.

## One schema, two authorities

### UI-managed (default)

Add, edit, disable or delete routes, enter/replace/delete secrets, and create,
edit grants for, or revoke callers. Secret deletion is refused while a route references it, including a
disabled route. Route names are immutable ACL identities: rename means delete and
create, with new grants. Concurrent updates use a revision check; refresh after a
conflict rather than overwriting another tab's changes.

Each grant is bound to the route name and a fingerprint of its **canonical
upstream origin, escaped base path and outbound header rules** (header names,
secret reference names, literal prefixes and nonsecret literal values). Changing
the base or a rule drops existing grants, even on the same origin; explicitly
regrant the intended callers in **Callers** after reviewing the new authority.
Deleting and recreating a route never restores its grants. Disabling a route
keeps its grants for later re-enabling. Changing only its local prefix,
local caller auth format or request-ID metadata does not widen upstream authority
and retains grants.

Routine secret-value rotation under the same name and origin retains grants;
actual secret bytes are **not** included in the fingerprint. The proxy cannot
infer whether a replacement key has wider upstream privileges. Treat replacement
as an intentional credential operation and continue using narrowly scoped keys.

Revoking callers, removing grants, disabling routes and deleting routes block
**new requests only**. Dispatched requests and streams can continue until they
finish, the client cancels, or their total timeout expires. Revocation is not
cancellation or proof that upstream work/billing stopped.

Secret-free YAML import/export uses the schema below. Imports cannot create file
references. A stored secret is bound to its normalized upstream origin (scheme,
host and effective port). Reusing it for another origin is rejected, including
changing the binding by replacement. Enter a differently named secret for a new
origin. UI secrets are **plaintext private server state**, not encrypted storage
or a vault.

### Operator-managed (`--config`)

```sh
term-llm serve proxy --state-dir ./proxy-state \
  --public-url https://proxy.example.test --config ./proxy.yaml
```

The file is authoritative and the route UI is read-only even if the mount is
writable. **Reload** validates the complete file and loads its secret files once,
then atomically activates the candidate. Editing a stored secret does not
implicitly reload unrelated file-source credentials. Invalid reloads retain the last valid
snapshot. No watcher, YAML writer, automatic merge or fallback to old UI routes
exists. Caller management and Requests remain available. An empty valid
configuration admits administration but forwards nothing.

Mode changes/reloads retain grants only for the same route name and exact
compatible authority fingerprint. Grants for missing or incompatible routes are
permanently removed, with a safe count printed at activation. Returning to an
older UI/file configuration does not resurrect them. Review **Callers** and
explicitly regrant access after switching modes; file mode is not a second
hidden grant namespace.

Only operator-authored configuration may reference files. File values are loaded
at activation, not per request; trailing CR/LF used as a text-file terminator is
removed. Admin-token, bootstrap/recovery files, passkey state and aliases/hardlinks to
them are forbidden as upstream material. Every file source requires an explicit `origin` (scheme, host and
effective port; no base path), normalized and enforced just like stored-secret
bindings. An absent or mismatched binding refuses activation. File sources
require private regular files and a maximum of 16 KiB. A trusted OS operator can change files/processes and is outside the browser's
confinement boundary.

### Configuration example

The hosts and references below are illustrative, not presets:

```yaml
routes:
  - name: service-a
    enabled: true
    local_prefix: /service-a/
    upstream: https://api.service-a.example/
    caller_auth:
      header: Authorization
      prefix: "Bearer "
    set_headers:
      Authorization:
        secret: service-a-key
        prefix: "Bearer "
    request_id_header: X-Request-ID

  - name: service-b
    enabled: true
    local_prefix: /service-b/
    upstream: https://api.service-b.example/unrelated/base/
    caller_auth:
      header: X-API-Key
    set_headers:
      X-Other-Credential:
        secret: service-b-key
      X-Client-Label:
        value: local-proxy

secrets:
  service-a-key:
    origin: https://api.service-a.example
    file: /run/secrets/service_a
  service-b-key:
    stored: service-b-key
```

Provision the second secret using UI-managed mode before switching explicitly to
an operator file that references it. The file names it without exposing its
value. A `stored` reference must match its declared secret name; there are no
mutable secret aliases. Its optional declared `origin` must match the stored
binding. Missing enabled-secret references reject startup or the whole candidate
reload, not silently substitute an empty credential. UI-managed exports omit
secret values too. File references in an operator export remain operator-only on
import. YAML `enabled` defaults to true; absent `caller_auth` defaults to
`Authorization` with literal prefix `Bearer `. A substitution contains **either**
`secret` with optional `prefix`, **or** a literal nonsecret `value`. There is no
suffix, expression language, interpolation, shell command or body rewriting.

HTTPS is required unless the route explicitly sets `allow_insecure_http: true`
for trusted private/testing traffic. Upstream URLs cannot contain userinfo,
queries or fragments. Local prefixes must be canonical, unique/non-overlapping,
non-root paths ending in `/`; administration, assets and health namespaces are
reserved. At most 128 routes, 256 secrets, 32 substitutions per route and 1 MiB of
configuration are accepted. Header names/values are validated; hop-by-hop,
framing, Host and proxy diagnostic targets are reserved.

## Exact forwarding contract

```text
/service-a/v1/anything  → https://api.service-a.example/v1/anything
/service-b/jobs/123     → https://api.service-b.example/unrelated/base/jobs/123
```

Header rules **replace**, never append. Local authentication requires one field
value with a literal case-sensitive prefix and nonempty caller token. Duplicate
auth/credential fields are rejected. The local auth header is removed even if the
upstream uses a different credential header. Configured substitutions always use
server-held values, not caller input. Outbound Host is the fixed upstream host.

The proxy strips Authorization, Proxy-Authorization, Cookie, Forwarded,
X-Forwarded-For/Host/Proto, X-Real-IP, X-Original-URL, X-Rewrite-URL,
X-Term-LLM-Proxy-* diagnostics and standard Connection-nominated/hop-by-hop
headers, before configured substitutions. Response Set-Cookie is removed. Other
end-to-end headers and opaque JSON, SSE, binary/media bodies pass through; no
transparent response decompression or response-body inspection occurs. Redirect
responses are returned, not followed or rewritten. Upstream responses may echo
credentials or refer to upstream URLs: opaque forwarding cannot guarantee a
malicious upstream will not disclose its own key.

Escaped path remainders join the escaped base with one boundary slash. Escapes
such as `%2F` in identifiers and **raw query bytes** are preserved, including query
fields Go's standard URL-query parser would reject. Query values are never logged.
Absolute-form targets, traversal/dot segments after decoding, backslashes/control
characters, CONNECT and Upgrade/WebSocket requests are rejected rather than
redirected. Validation rejects nested traversal encodings within the supported
normalization contract; it does not claim to secure upstreams with arbitrary
nonstandard recursive normalization. There is no forward-proxy or MITM mode.

No retry/fallback/replay occurs after ambiguous dispatch. Cancellation or a broken
stream can leave an uncertain upstream outcome, including whether billing/work
occurred. Caller revocation/route changes block new admissions; they cannot undo
already dispatched work.

## Requests and durability

Requests shows caller, route/revision, method, bounded slash-prefixed relative
path and its recorded local prefix, actual peer
IP, status/outcome, sent/received bytes and header/first-body-byte/total timings,
plus the optional configured upstream request ID. Byte counters measure body
bytes read from the client and written to the response writer, not proof of
upstream/client receipt. The generated `px_…` request ID
is also returned in `X-Term-LLM-Proxy-Request-ID`. No bodies, arbitrary header
capture, full URLs, models, token counts or cost accounting are stored.

Filter by time, caller, route, method, path and outcome; path filters accept
`/relative/path` or the full `/local-prefix/relative/path` for new records. The UI
shows the full recorded local route path. Older acceptance-build records that
lack a prefix remain honest relative paths rather than guessing from a route
that may have changed. Open inline row details, copy IDs,
page backward using a stable sequence cursor or download bounded JSON (at most
1,000 matching rows per export). Default retention is 30 days, deleted in small
batches. At high sustained traffic the bounded cleanup can lag; operators must
monitor disk capacity. Unauthenticated/rejected traffic does not create ledger
rows. The active set is bounded by concurrency.

A start row commits **before dispatch**. If it cannot commit, the request gets 503
and the upstream is not called. Terminal updates use an independent bounded
context. Failed terminal writes, crashes or old incomplete rows remain **unknown**,
not fabricated successes. Active work is distinguished from unknown historical
work. A transport error, timeout or partial stream is not evidence of zero
upstream work; do not automatically replay it.

The store uses SQLite **WAL / synchronous=NORMAL**. It supports process-crash
attribution but recent records may be lost on host power loss/kernel failure. It
is not FULL-fsync durability, exactly-once billing or tamper-proof history. Do not
copy just a live `.db` file and ignore its WAL: stop the process or use a
consistent SQLite backup procedure, protecting secrets in backups as carefully as
production state. The proxy schema is versioned independently of sessions.

Schema v2 upgrades the initial unreleased v1 acceptance build's origin-only
grants to exact-authority grants. Since their original base/header scope cannot
be proven retrospectively, the one-time migration removes those grants while
preserving caller names, token hashes, secrets, routes and history. Regrant access
in **Callers**; clients can retain their existing local tokens. Do not run an old
writer against v2, and restore a protected backup to downgrade.

Known secrets/caller token values and the shared secret-text detector redact
metadata paths. Heuristics cannot guarantee removal of arbitrary secrets embedded
in path identifiers. Do not proxy APIs that encode sensitive material in paths
without accepting this limitation. Query values are excluded entirely. Upstream
request-ID metadata is bounded and redacted too.

## Administration and operations

Admin sessions use HttpOnly, SameSite=Strict cookies scoped to `/_proxy/`, Secure
on HTTPS, with separate CSRF values. All mutations require the configured exact
Origin and session CSRF (or a pre-authentication CSRF cookie/value for passkey
setup/login). There is no query authentication, browser localStorage secret or raw
host-secret cookie. Passkey challenges are one-use and bound to ceremony kind,
host enrollment grant or session and user identity. Invalid host-secret guesses
are throttled with bounded peer tracking; valid strong secrets cannot be locked
out by another client sharing the reverse proxy's peer IP.

Unauthenticated login challenges have a separate bounded registry from all
session/grant-protected ceremonies. At public-login capacity, a new begin replaces
the oldest pending login (for the shared peer, or globally when only that bound
is full); evicted challenges fail closed and every challenge is still one-use.
This avoids a fixed five-minute capacity lockout behind a TLS terminator without
letting public login consume reauthentication/enrollment/recovery capacity.
A sustained flood can still evict a legitimate pending login or consume network /
CPU resources: this is not DoS immunity. Apply appropriate network restrictions,
connection/rate controls and monitoring at the TLS terminator. Token compatibility
sessions expire after eight hours, have a 64-session bound and are also invalidated
on restart. Invalid token guesses use the same bounded lockout-resistant policy.

### HTTPS termination and hostname routing

The proxy has **no built-in TLS**. For remote use, configure a stable
`--public-url https://proxy.example.test` and put an externally managed TLS reverse
proxy in front of a **private** HTTP listener. Route the **whole hostname at its
root**, preserve the public Host header and pass paths unchanged: `/` redirects to
`/_proxy/`, while caller routes and `/healthz` remain at the root. Mounting the
application under a rewritten subpath is unsupported. The reverse proxy should
expose readiness only where appropriate. Proxy startup does not provision DNS,
issue certificates, install trust roots or manage that reverse proxy.

Host and Origin must match the configured origin exactly (including non-default
port); `Forwarded`, `X-Forwarded-Host` and `X-Forwarded-Proto` are **not trusted**.
TLS is determined by explicit public-url policy, not arbitrary forwarded headers.
Only the declared localhost HTTP origin gets non-Secure development cookies. In
explicit token mode without public-url, only loopback browser Hosts are accepted
(including SSH forwarded ports). Host is not authentication: network clients can
spoof it. Caller routes are not restricted to browser admin Hosts, so protect their
network path with TLS too. Do not expose the private backend as an alternate entry.

A minimal Caddy configuration (run/manage it separately) is:

```caddyfile
proxy.example.test {
    reverse_proxy 127.0.0.1:8082 {
        # Preserve streaming/SSE delivery; do not buffer an entire response.
        flush_interval -1
    }
}
```

Use a real domain and publicly trusted certificate issuer for real browsers;
Caddy's normal public ACME flow requires appropriate DNS/reachability and operator
approval. `.test` names are examples, not publicly issued domains. An isolated
deployment test can instead supply an ephemeral private-CA certificate trusted
**only in the test browser**; this does not prove public certificate issuance.
Do not disable HTTPS verification. Chromium virtual WebAuthn authenticators test
protocol/browser integration, not physical-device support or vendor attestation
(the shared engine requests no attestation). On all terminators, disable response
buffering for SSE, preserve cancellation, and choose response/idle/total timeouts
long enough for the proxy's configured streaming deadline. Do not add automatic
upstream retries for these authenticated requests.

The standalone command and its health subcommand refuse raw-debug and CPU/heap/
pprof options, including `TERM_LLM_PPROF`, before those facilities can start. It
also bypasses automatic update/config initialization. Advisory process discovery
may use a private `XDG_RUNTIME_DIR`; container examples put it on temporary storage.

```sh
term-llm serve proxy health --host 127.0.0.1 --port 8082
```

Health contacts only the local listener/store; it does not test upstream service
availability. Default bounds are 64 active requests (no unbounded admission queue),
64 KiB incoming headers, 64 MiB streamed request bodies, 15-second dial/TLS/header
read bounds and a configurable 15-minute **total** forwarding deadline. There is
no short upstream response-header deadline. Tune `--max-concurrent`,
`--max-body-bytes`, `--request-timeout` and `--retention-days` deliberately. Incoming
chunked bodies are limited while reading; an over-limit partial dispatch has an
uncertain outcome, never an automatic retry. A full disk prevents new dispatches
when start commits fail; transient terminal-write errors do not permanently latch
the service down. Shutdown stops admission, drains for 20 seconds, then cancels
unfinished work with bounded terminal bookkeeping.

For static/nonroot OCI and Compose preparation, see
[`examples/proxy`](https://github.com/term-llm/term-llm/tree/main/examples/proxy).
This feature does not publish an image or automatically isolate a client's egress.
Keep real upstream keys/proxy state out of client containers; root/host/Docker
access defeats that isolation. Use HTTPS for remote/untrusted callers. A shared
private network alone is not encryption or an egress firewall.

For a local-only demonstration, `python3 examples/proxy/mock_upstreams.py` starts
two synthetic servers with unrelated auth headers and bases. It prints their
nonsecret fixture credentials; use UI-managed routes to connect them. No provider
credentials, billable calls or registry access are needed.

For source-level browser acceptance against an already running isolated fixture,
`scripts/proxy_passkey_smoke.sh --help` describes a reusable URL/private-state/
secret-file driven Chromium virtual-WebAuthn smoke. Its `enroll`, `restart` and
`recovery` phases require operator-controlled server lifecycle. It keeps HTTPS
verification enabled and never installs trust or starts/stops the server. Optional
synthetic route/caller-file inputs check machine-only forwarding. Use disposable
fixtures only; saved browser cookies and virtual private credentials are private
test artifacts, not production backup material.
