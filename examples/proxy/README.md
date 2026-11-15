# Standalone credential proxy deployment

See [the user guide](../../docs-site/content/guides/credential-proxy.md) for routes,
security and operating modes. Nothing here installs, starts, publishes or deploys
an image automatically. `mock_upstreams.py` runs only two synthetic localhost test
servers; it does not call a provider.

## Release-based image context

Use a release **containing this feature** built with `CGO_ENABLED=0`. Do not use an
installed dynamically linked binary. From the repository root:

```sh
scripts/prepare-proxy-image.sh vVERSION ./tmp/proxy-image-VERSION
# Review the pinned distroless manifest digest before a release.
# Explicitly authorized build/publication may use:
# docker buildx build --platform linux/amd64,linux/arm64 ./tmp/proxy-image-VERSION
```

The preparation script downloads only the two named Linux release archives and
checksums from the canonical GitHub release, verifies both archive hashes, extracts
only the binaries and rejects binaries whose Go build settings are not static.
Checksums authenticate against that GitHub release, not an independent signature.
The runtime has CA certificates, no shell, and runs as UID/GID 65532. No agent
workspace, browser, SDK, Docker socket or host secret directory is included.

The versioned canonical image name is intended to be
`ghcr.io/term-llm/term-llm:<version>`; **this change does not publish an image or add a
registry-write job**. Registry authorization and actual restricted-runtime,
multi-architecture/resolver/TLS smoke verification are release-owner gates before
publication. Do not claim the image exists until published. A future isolated
post-release job should consume these checksum-verified archives with no npm,
source builds or unrelated signing secrets and publish immutable version/digest
references only after smoke verification.

## Prepare bind mounts explicitly

From a new private deployment directory, with paths adjusted to the operator's
policy (these privileged commands are documentation, not startup hooks):

```sh
install -d -m 0700 state secrets
(umask 077; openssl rand -hex 32 > secrets/bootstrap)
sudo chown 65532:65532 state secrets/bootstrap
# The parent secrets directory must permit traversal by the container UID.
# A root-owned 0711 directory exposes no listing; bootstrap itself remains 0600.
chmod 0711 secrets
cp /path/to/checkout/examples/proxy/compose.yaml .
export PROXY_PUBLIC_URL=https://proxy.example.test
export PROXY_IMAGE_CONTEXT=/absolute/path/to/verified/context
# With explicit operator authorization:
# docker compose up --build
```

Docker rootless/user-namespace remapping changes ownership mapping: provision and
verify the actual mapped UID rather than assuming host UID 65532. Test secret file
readability with the real Compose runtime. Compose's secret `uid`/`mode` settings
are not a portable fix for bind mount ownership; this example deliberately uses an
explicit private read-only file mount. Never recursively chown on startup.

Passkey administration is the default. The Compose command requires an exact
`PROXY_PUBLIC_URL` HTTPS domain origin and an independently managed TLS terminator
(Caddy/nginx/etc.) in front of the private, loopback-published HTTP port. There is
**no built-in TLS**, certificate issuance, DNS setup or automatic terminator
startup. On Docker Desktop or a separate reverse-proxy container, connect using an
explicit private network instead of assuming its loopback reaches this host port.
Preserve the public Host and route **all root paths** unchanged: admin `/_proxy/`,
caller routes and readiness `/healthz`. No rewritten subpath mounting or trust in
`Forwarded`/`X-Forwarded-*` headers. Keep the backend unreachable from untrusted
clients, and do not buffer SSE; align terminator timeouts with `--request-timeout`.
The proxy still needs upstream egress: a private Docker network is not automatic
client egress isolation.

Open `${PROXY_PUBLIC_URL}/_proxy/`, exchange the contents of `secrets/bootstrap`,
and enroll a named browser passkey. The secret can be exchanged once within ten
minutes of startup for a five-minute enrollment grant. It is not an admin password
or a machine caller credential. **After enrollment remove the bootstrap command
arguments and file mount**, then securely remove the host file. Retain the public
URL and `/state` bind mount on normal restarts. Named credentials persist under
`/state/admin-auth/`; browser sessions do not survive restarts. Keep the hostname
stable, use HTTPS for remote callers too, and provision a spare passkey.

For host-assisted recovery, explicitly restart with a freshly provisioned private
file mounted read-only and `--passkey-recovery-token-file /run/secrets/recovery`
instead of the bootstrap flag. Recovery adds a key, revokes all sessions and then
requires passkey login. Remove that flag/file after use; old keys must be revoked
explicitly in **Passkeys** after login. Neither flow changes machine callers.

For intentional local/legacy token administration, override the Compose command
with `--auth token --admin-token-file /run/secrets/proxy_admin` and individually
mount a fresh private admin file at that path. Remove passkey file flags/mounts.
There is no silent token fallback in passkey mode. `http://localhost[:port]` is the
only passkey HTTP development exception; use that exact Host, not an IP address.
For tests, a private CA can be trusted in an isolated browser without changing
system trust. That proves neither publicly trusted ACME issuance nor hardware
passkey/attestation support. Never use an ignore-HTTPS-errors workaround. See the
user guide for a minimal separately managed Caddy example and complete lifetimes.

Normal mode is UI-managed, persisted under `/state`. For operator-managed routes,
mount a private YAML file read-only and add `--config /config/proxy.yaml` to the
command, retaining the other arguments. Secret files must also be individually
mounted read-only and readable only by the runtime UID. The UI cannot introduce
file references. Protect `/state` including database WAL/SHM files and backups;
write-only secrets are plaintext at rest, not a vault.

Readiness uses `serve proxy health` and contacts only the local store/listener.
There is no upstream billable probe. Rootfs is read-only, capabilities dropped,
privilege escalation disabled, temporary storage bounded, and graceful stop is 30
seconds (20-second drain plus bounded terminal bookkeeping). Never mount proxy
state or real upstream credentials into client/agent containers.

## Reusable browser acceptance smoke

From a checkout with existing frontend dependencies/Chromium,
`scripts/proxy_passkey_smoke.sh --help` describes the **external-endpoint-only**
WebAuthn smoke. Supply `TERM_LLM_PROXY_PASSKEY_URL`, a private synthetic-artifact
`TERM_LLM_PROXY_PASSKEY_STATE`, `TERM_LLM_PROXY_PASSKEY_PHASE=enroll|restart|recovery`
and the appropriate explicit bootstrap/recovery file. The operator controls the
server restarts between phases. It exercises real UI enrollment/login/logout,
Secure-cookie policy, restart invalidation and host-assisted recovery using a
Chromium virtual authenticator. Optional synthetic route/caller-file inputs check
that browser sessions do not authorize forwarding but machine caller tokens do.

The wrapper does not start or stop services, install dependencies, issue
certificates, modify trust or disable HTTPS validation. For a private-CA HTTPS
fixture, provision trust independently in the test browser; the optional separate
Node machine probe also needs its own CA trust (for example `NODE_EXTRA_CA_CERTS`).
Saved virtual credentials and cookies are sensitive disposable test artifacts:
never point the smoke at production, a real provider, an agent workspace, or real
credentials. It is not physical-device, attestation or public-issuer evidence.
