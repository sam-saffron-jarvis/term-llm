#!/usr/bin/env bash
# External-only smoke: no builds, server/Caddy lifecycle, trust changes or installs.
set -euo pipefail
umask 077

usage() {
  cat <<'USAGE'
Run one phase against a caller-managed synthetic proxy fixture:

  TERM_LLM_PROXY_PASSKEY_URL=https://proxy.example \
  TERM_LLM_PROXY_PASSKEY_STATE=/private/proxy-smoke \
  TERM_LLM_PROXY_PASSKEY_PHASE=enroll \
  TERM_LLM_PROXY_PASSKEY_BOOTSTRAP_FILE=/private/bootstrap \
    scripts/proxy_passkey_smoke.sh

Then restart the proxy yourself (same persisted credential store) and run PHASE=restart
with the same STATE. Run PHASE=recovery with RECOVERY_FILE pointing to a fresh
private recovery secret configured by the caller. All abbreviated variable names
here use the TERM_LLM_PROXY_PASSKEY_ prefix. URL accepts an origin or /_proxy/.
Secrets are read from explicit files, never token environment values. STATE holds
synthetic virtual credentials and browser cookies as mode-0600 JSON in a mode-0700
directory; preserve it between phases, and remove it yourself when finished.
The STATE parent must already exist. Existing non-private paths and symlinks are
rejected, never chmodded. Each invocation creates a fresh private results directory.

Optional ROUTE_PATH=/synthetic/endpoint plus CALLER_TOKEN_FILE=/private/caller
checks cookie-only forwarding denial and Authorization: Bearer caller success.
The caller must supply a harmless GET endpoint returning 2xx, not a real provider.
The separate machine API client also needs Node TLS trust (for example a caller-set
NODE_EXTRA_CA_CERTS pointing to the private CA); no browser cookies are copied.

Prerequisites: existing frontend dependencies and a caller-provisioned Chromium
(PLAYWRIGHT_CHROMIUM_EXECUTABLE or Playwright's existing browser), reachable proxy,
and any required Caddy/private-CA trust already installed in the browser HOME.
TLS certificate validation is ALWAYS enabled, including secondary contexts.
This is a virtual-authenticator smoke, not hardware, attestation or public issuance
validation. No servers are started/stopped and no trust or dependencies are installed.
USAGE
}
if [[ "${1:-}" == --help ]]; then usage; exit 0; fi
if [[ $# != 0 ]]; then usage >&2; exit 2; fi
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ ! -x "$root/frontend/node_modules/.bin/playwright" ]]; then
  echo "frontend smoke dependencies are missing; provision them before this external smoke" >&2
  exit 1
fi
for name in TERM_LLM_PROXY_PASSKEY_URL TERM_LLM_PROXY_PASSKEY_STATE TERM_LLM_PROXY_PASSKEY_PHASE; do
  if [[ -z "${!name:-}" ]]; then echo "Missing required input: $name" >&2; exit 2; fi
done
case "$TERM_LLM_PROXY_PASSKEY_PHASE" in
  enroll) required_file=TERM_LLM_PROXY_PASSKEY_BOOTSTRAP_FILE ;;
  restart) required_file= ;;
  recovery) required_file=TERM_LLM_PROXY_PASSKEY_RECOVERY_FILE ;;
  *) echo "PHASE must be enroll, restart or recovery" >&2; exit 2 ;;
esac
if [[ -n "$required_file" && -z "${!required_file:-}" ]]; then
  echo "Missing required private fixture file: $required_file" >&2
  exit 2
fi
if [[ -n "${TERM_LLM_PROXY_PASSKEY_ROUTE_PATH:-}" || -n "${TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE:-}" ]]; then
  if [[ -z "${TERM_LLM_PROXY_PASSKEY_ROUTE_PATH:-}" || -z "${TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE:-}" ]]; then
    echo "Optional forwarding requires both ROUTE_PATH and CALLER_TOKEN_FILE" >&2
    exit 2
  fi
fi

# Resolve paths before changing directory; leave the caller's HOME and trust intact.
export TERM_LLM_PROXY_PASSKEY_STATE="$(node -e 'console.log(require("node:path").resolve(process.argv[1]))' "$TERM_LLM_PROXY_PASSKEY_STATE")"
for name in TERM_LLM_PROXY_PASSKEY_BOOTSTRAP_FILE TERM_LLM_PROXY_PASSKEY_RECOVERY_FILE TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE; do
  if [[ -n "${!name:-}" ]]; then
    value="$(node -e 'console.log(require("node:path").resolve(process.argv[1]))' "${!name}")"
    printf -v "$name" '%s' "$value"
    export "$name"
  fi
done
node <<'JS'
const fs = require('node:fs');
const path = require('node:path');
const env = process.env;
function fail(message) { console.error(message); process.exit(2); }
let url;
try { url = new URL(env.TERM_LLM_PROXY_PASSKEY_URL); } catch { fail('Invalid proxy URL'); }
if (url.username || url.password || url.search || url.hash ||
    !['/', '/_proxy', '/_proxy/'].includes(url.pathname) ||
    !(url.protocol === 'https:' || (url.protocol === 'http:' && url.hostname === 'localhost'))) {
  fail('Use an HTTPS origin or /_proxy/ URL; HTTP is allowed only on localhost');
}
const routePath = env.TERM_LLM_PROXY_PASSKEY_ROUTE_PATH;
if (routePath) {
  const route = new URL(routePath, url.origin);
  if (!routePath.startsWith('/') || routePath.startsWith('//') || route.origin !== url.origin || route.pathname.startsWith('/_proxy')) {
    fail('ROUTE_PATH must be a same-origin machine route, not administration');
  }
}
const state = env.TERM_LLM_PROXY_PASSKEY_STATE;
const existing = fs.lstatSync(state, { throwIfNoEntry: false });
if (!existing) fs.mkdirSync(state, { mode: 0o700 });
const directory = fs.lstatSync(state);
if (!directory.isDirectory() || directory.isSymbolicLink() || (directory.mode & 0o077) !== 0) {
  fail('STATE must be a private directory (0700 or stricter), not a symlink');
}
function privateFile(file, limit, required = true) {
  const stat = fs.lstatSync(file, { throwIfNoEntry: false });
  if (!stat && !required) return;
  if (!stat?.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077) !== 0 || stat.size > limit) {
    fail('Fixture/output files must be bounded private regular files, not symlinks');
  }
  return stat;
}
for (const name of ['browser.json', 'credentials.json']) privateFile(path.join(state, name), 65536, false);
const phase = env.TERM_LLM_PROXY_PASSKEY_PHASE;
const files = phase === 'enroll' ? [[env.TERM_LLM_PROXY_PASSKEY_BOOTSTRAP_FILE, 4096]] :
  phase === 'recovery' ? [[env.TERM_LLM_PROXY_PASSKEY_RECOVERY_FILE, 4096]] : [];
if (env.TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE) files.push([env.TERM_LLM_PROXY_PASSKEY_CALLER_TOKEN_FILE, 4096]);
if (phase !== 'enroll') files.push([path.join(state, 'browser.json'), 65536], [path.join(state, 'credentials.json'), 65536]);
for (const [file, limit] of files) {
  try {
    const expected = privateFile(file, limit);
    const fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
    try {
      const actual = fs.fstatSync(fd);
      if (!actual.isFile() || (actual.mode & 0o077) !== 0 || actual.size > limit ||
          actual.ino !== expected.ino || actual.dev !== expected.dev) fail('Unsafe fixture file');
      const size = fs.readSync(fd, Buffer.alloc(limit + 1), 0, limit + 1, 0);
      if (!size || size > limit) fail('Fixture file is empty or too large');
    } finally { fs.closeSync(fd); }
  } catch { fail('A required private fixture file is missing, unsafe or unreadable'); }
}
JS

output="$(mktemp -d "$TERM_LLM_PROXY_PASSKEY_STATE/results-$TERM_LLM_PROXY_PASSKEY_PHASE.XXXXXX")"

cd "$root/frontend"
# The shared config's serial/parallel filters target chat fixtures, not this endpoint.
unset TERM_LLM_E2E_PHASE
export TERM_LLM_E2E_ARTIFACTS=off
exec ./node_modules/.bin/playwright test e2e/proxy-passkey.spec.ts \
  --project=desktop --workers=1 --retries=0 \
  --output="$output"
