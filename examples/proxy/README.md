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
(umask 077; openssl rand -hex 32 > secrets/admin)
sudo chown 65532:65532 state secrets/admin
# The parent secrets directory must permit traversal by the container UID.
# A root-owned 0711 directory exposes no listing; admin itself remains 0600.
chmod 0711 secrets
cp /path/to/checkout/examples/proxy/compose.yaml .
export PROXY_IMAGE_CONTEXT=/absolute/path/to/verified/context
# With explicit operator authorization:
# docker compose up --build
```

Docker rootless/user-namespace remapping changes ownership mapping: provision and
verify the actual mapped UID rather than assuming host UID 65532. Test secret file
readability with the real Compose runtime. Compose's secret `uid`/`mode` settings
are not a portable fix for bind mount ownership; this example deliberately uses an
explicit private read-only file mount. Never recursively chown on startup.

The loopback published port protects the default admin entry. SSH forwarding works
with loopback Host headers. For remote access use an explicitly configured HTTPS
reverse proxy (`--public-url https://proxy.example.test`) and TLS for callers, not
untrusted plaintext networking. The proxy still needs upstream egress: a private
Docker network is not automatic client egress isolation.

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
