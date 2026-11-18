#!/usr/bin/env python3
"""Black-box token/admin + HTTP/SSE parity, using two already-built CLI artifacts.

Usage: token_parity_test.py PINNED_GO_CLI RUST_CLI_BUNDLE
Run on the shared runner, with only loopback networking enabled. No builds, live
providers, inherited credentials, Rust-only environment auth, or fake passkeys.
Exit 0: parity; 1: candidate differences; 2: invalid oracle/control/fixture/run.

Both binaries use `serve proxy --auth token --config ... --state-dir ...
--admin-token-file ... --port 0` and the Go `Proxy ready · http://127.0.0.1:PORT`
stdout marker. A fresh Go-vs-Go control runs before the candidate. Reports contain
only fixed expectations, status codes and predicates, never tokens or raw logs.
Only generated IDs/keys/CSRF/cookies and timestamps are checked by explicit shape
and relationship; ephemeral origins are derived from each process's ready port.
Admin state is an explicitly selected projection, not a whole-state parity claim.
Persistence/restart, passkeys, request ledger and other transports are NOT tested.
"""

import contextlib
import hashlib
import http.client
import http.cookies
import http.server
import json
from pathlib import Path
import queue
import re
import subprocess
import sys
import tempfile
import threading
import time

from parity_test import CLI, Probe, TIMEOUT


START_TIMEOUT = 15
READY = re.compile(rb"Proxy ready \xc2\xb7 http://127\.0\.0\.1:([0-9]+)\r?\n")
API = "/_proxy/api/"
FIRST = b'data: {"part":1}\n\n'
SECOND = b'data: {"part":2}\n\ndata: [DONE]\n\n'
HTTP_BODY = b'{"mock":"byte-for-byte"}\n'
REQUEST_BODY = b'{"model":"fixture","stream":true}'
CASES = (
    "startup", "auth status", "anonymous admin denied", "admin bearer is not a session",
    "login requires origin", "login rejects foreign origin", "invalid admin token",
    "login", "authenticated state", "mutation requires CSRF", "mutation rejects wrong CSRF",
    "mutation rejects foreign origin", "issue rejects unknown grant", "issue caller",
    "caller absent credential", "caller invalid credential", "admin key cannot forward",
    "remove grant", "removed grant denied", "restore grant", "authenticated HTTP",
    "authenticated gated SSE", "revoke caller", "revoked caller denied",
    "revoked caller cannot be regranted", "logout requires CSRF", "logout",
    "logged out session denied", "auth status after logout", "upstream accounting",
    "graceful shutdown",
)


class ScenarioFailure(Exception):
    """A safe, already-recorded prerequisite failure (no payload in exception)."""


class Fixture(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), Handler)
        self.requests = queue.Queue()
        self.errors = queue.Queue()
        self.release = threading.Event()
        self.first_sent = threading.Event()
        self.second_sent = threading.Event()
        self.expected_vendor = None
        self.caller_token = None

    def handle_error(self, *_):
        self.errors.put("fixture exception")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass

    def do_GET(self):
        self.respond()

    def do_POST(self):
        self.respond()

    def respond(self):
        self.connection.settimeout(TIMEOUT)
        self.close_connection = True
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        # Store predicates, not credential bytes. Substitution is an absolute
        # fixture assertion, not merely a Go/Rust relative comparison.
        auth = self.headers.get_all("Authorization", [])
        headers = self.headers.as_bytes()
        self.server.requests.put({
            "get_models": self.command == "GET" and self.path == "/backend/v1/models?fixture=1",
            "post_stream": self.command == "POST" and self.path == "/backend/v1/chat/completions?stream=1",
            "vendor_only": auth == ["Bearer " + self.server.expected_vendor],
            "caller_absent": self.server.caller_token.encode() not in headers + body + self.path.encode(),
            "body_exact": body == (REQUEST_BODY if self.command == "POST" else b""),
            "query_exact": self.path in ("/backend/v1/models?fixture=1",
                                         "/backend/v1/chat/completions?stream=1"),
        })
        stream = self.path == "/backend/v1/chat/completions?stream=1"
        self.send_response(200 if stream else 207)
        self.send_header("Content-Type", "text/event-stream" if stream else "application/json")
        self.send_header("Set-Cookie", "synthetic=discard")
        self.send_header("Connection", "close")
        if not stream:
            self.send_header("Content-Length", str(len(HTTP_BODY)))
        self.end_headers()
        if not stream:
            self.wfile.write(HTTP_BODY)
            return
        self.wfile.write(FIRST)
        self.wfile.flush()
        self.server.first_sent.set()
        if not self.server.release.wait(3 * TIMEOUT):
            self.server.errors.put("SSE gate was not released")
            return
        self.wfile.write(SECOND)
        self.wfile.flush()
        self.server.second_sent.set()


@contextlib.contextmanager
def fixture():
    server = Fixture()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.release.set()
        server.shutdown()
        server.server_close()
        thread.join(TIMEOUT)


class Process:
    def __init__(self, probe, config):
        self.probe = probe
        self.command = [str(probe.binary), *CLI, "--auth", "token", "--config", str(config),
                        "--state-dir", str(probe.home / "proxy-state"),
                        "--admin-token-file", str(probe.token), "--port", "0"]
        self.ports = queue.Queue()
        self.thread = None
        self.proc = None

    def __enter__(self):
        self.proc = subprocess.Popen(self.command, cwd=self.probe.home, env=self.probe.env,
                                     stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL)

        def drain():
            with self.proc.stdout:
                for line in self.proc.stdout:
                    match = READY.fullmatch(line)
                    if match:
                        self.ports.put(int(match[1]))
            self.ports.put(None)

        self.thread = threading.Thread(target=drain, daemon=True)
        self.thread.start()
        return self

    def ready(self):
        port = self.ports.get(timeout=START_TIMEOUT)
        if port is None or not 0 < port < 65536:
            raise ScenarioFailure()
        return port

    def stop(self):
        self.proc.terminate()
        return self.proc.wait(timeout=TIMEOUT)

    def __exit__(self, *_):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=TIMEOUT)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=TIMEOUT)
        self.thread.join(TIMEOUT)


def request(port, method, path, headers=None, body=None):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=TIMEOUT)
    try:
        conn.request(method, path, body=body, headers=headers or {})
        res = conn.getresponse()
        return res.status, res.headers, res.read()
    finally:
        conn.close()


def shape(value, prefix, digits):
    return isinstance(value, str) and re.fullmatch(re.escape(prefix) + "[0-9a-f]{%d}" % digits, value) is not None


def run(binary, home):
    results = {}
    active = "startup"

    def record(name, observed, expected, prerequisite=False):
        nonlocal active
        active = name
        results[name] = {"observed": observed, "expected": expected, "ok": observed == expected}
        if prerequisite and observed != expected:
            raise ScenarioFailure()

    probe = Probe(binary, home)
    # Probe is reused only for isolation and private token files. Do NOT enable
    # the legacy Rust environment-token entrypoint from its startup smoke helper.
    probe.env.pop("TERM_LLM_RUST_PROXY_TOKEN", None)
    with fixture() as upstream:
        upstream.expected_vendor = probe.vendor.read_text()
        upstream.caller_token = "not-issued-yet"
        config = probe.config(upstream.server_port)
        cfg = json.loads(config.read_text())
        cfg["routes"][0]["allow"] = ["GET /v1/models", "POST /v1/chat/completions"]
        config.write_text(json.dumps(cfg), encoding="utf-8")
        try:
            with Process(probe, config) as proc:
                port = proc.ready()
                record("startup", True, True)
                origin = "http://127.0.0.1:%d" % port
                session = {}

                def api(method, path, value=None, headers=None, authenticated=False):
                    nonlocal active
                    # Fixed-response cases evaluate the HTTP call before record().
                    # Keep transport failures attributed to the next named case.
                    if active in results:
                        active = next((name for name in CASES if name not in results), active)
                    h = {"Content-Type": "application/json"}
                    if method not in ("GET", "HEAD"):
                        h["Origin"] = origin
                    if authenticated:
                        h.update(session)
                    h.update(headers or {})
                    return request(port, method, API + path, h,
                                   None if value is None else json.dumps(value).encode())

                def fixed(name, response, status, payload, admin=True):
                    code, headers, body = response
                    record(name, {"status": code, "body_exact": body == payload,
                                  "no_store": headers.get("Cache-Control") == "no-store"},
                           {"status": status, "body_exact": True, "no_store": admin})

                status_body = b'{"auth":"token","csrf":"","origin":""}\n'
                # JSON key order/whitespace is not part of the admin JSON contract.
                def auth_status(name):
                    nonlocal active
                    active = name
                    code, headers, body = api("GET", "auth/status")
                    record(name, {"status": code, "json_exact": json.loads(body) == json.loads(status_body),
                                  "no_store": headers.get("Cache-Control") == "no-store"},
                           {"status": 200, "json_exact": True, "no_store": True})

                auth_status("auth status")
                denied = b'{"error":"Sign in to administer this proxy"}\n'
                fixed("anonymous admin denied", api("GET", "state"), 401, denied)
                fixed("admin bearer is not a session", api("GET", "state", headers={
                    "Authorization": "Bearer " + probe.token.read_text()}), 401, denied)
                login = {"token": probe.token.read_text()}
                fixed("login requires origin", api("POST", "login", login, {"Origin": ""}),
                      403, b'{"error":"A matching Origin is required"}\n')
                foreign = {"Origin": "https://foreign.invalid"}
                cross = b'{"error":"Cross-origin administration is not allowed"}\n'
                fixed("login rejects foreign origin", api("POST", "login", login, foreign), 403, cross)
                fixed("invalid admin token", api("POST", "login", {"token": "invalid-fixture"}),
                      401, b'{"error":"Admin credential not recognized"}\n')
                active = "login"
                code, headers, body = api("POST", "login", login)
                data = json.loads(body)
                cookies = http.cookies.SimpleCookie()
                cookies.load(headers.get("Set-Cookie", ""))
                cookie = cookies.get("tlp_admin")
                csrf = data.get("csrf")
                record("login", {"status": code, "csrf_shape": shape(csrf, "csrf_", 64),
                                 "json_keys_exact": set(data) == {"csrf"}, "cookie_shape": cookie is not None and shape(cookie.value, "admin_", 64),
                                 "cookie_attributes": cookie is not None and cookie["path"] == "/_proxy/" and bool(cookie["httponly"])
                                 and cookie["samesite"].lower() == "strict" and not cookie["secure"] and cookie["max-age"] == "28800",
                                 "single_cookie": len(headers.get_all("Set-Cookie", [])) == 1,
                                 "no_store": headers.get("Cache-Control") == "no-store"},
                       {"status": 200, "csrf_shape": True, "json_keys_exact": True, "cookie_shape": True,
                        "cookie_attributes": True, "single_cookie": True, "no_store": True}, True)
                session = {"Cookie": "tlp_admin=" + cookie.value, "X-CSRF-Token": csrf}
                active = "authenticated state"
                code, _, body = api("GET", "state", authenticated=True)
                state = json.loads(body)
                revision = state.get("revision")
                record("authenticated state", {"status": code, "mode_exact": state.get("mode") == "file", "auth_exact": state.get("auth") == "token",
                                               "csrf_relationship": state.get("csrf") == csrf,
                                               "empty_callers": state.get("callers") == [],
                                               "revision_exact": type(revision) is int and revision == 1},
                       {"status": 200, "mode_exact": True, "auth_exact": True, "csrf_relationship": True,
                        "empty_callers": True, "revision_exact": True}, True)
                new_caller = {"name": "fixture caller", "grants": ["mock"], "revision": revision}
                csrf_error = b'{"error":"Session changed; refresh and try again"}\n'
                fixed("mutation requires CSRF", api("POST", "callers", new_caller,
                      {"X-CSRF-Token": ""}, True), 403, csrf_error)
                fixed("mutation rejects wrong CSRF", api("POST", "callers", new_caller,
                      {"X-CSRF-Token": "wrong-fixture"}, True), 403, csrf_error)
                fixed("mutation rejects foreign origin", api("POST", "callers", new_caller, foreign, True), 403, cross)
                fixed("issue rejects unknown grant", api("POST", "callers", new_caller | {"grants": ["missing"]},
                      authenticated=True), 400,
                      b'{"error":"route grant is not in the active configuration; refresh before saving"}\n')
                active = "issue caller"
                before = time.time_ns() // 1000000
                code, _, body = api("POST", "callers", new_caller, authenticated=True)
                after = time.time_ns() // 1000000
                issued = json.loads(body)
                caller = issued.get("caller", {})
                token = issued.get("token")
                caller_id = caller.get("id")
                created = caller.get("created")
                fixed_caller = {"name": "fixture caller", "last_used": 0, "revoked": False,
                                "grants": ["mock"], "created_by": "", "external_id": ""}
                record("issue caller", {"status": code, "keys_exact": set(issued) == {"caller", "token"},
                       "token_shape": shape(token, "sk-tlp-", 64), "id_shape": shape(caller_id, "c_", 24),
                       "created_in_request_window": type(created) is int and before <= created <= after,
                       "fixed_caller_exact": {k: v for k, v in caller.items() if k not in ("id", "created")} == fixed_caller},
                       {"status": 201, "keys_exact": True, "token_shape": True, "id_shape": True,
                        "created_in_request_window": True, "fixed_caller_exact": True}, True)
                upstream.caller_token = token
                auth = {"Authorization": "Bearer " + token}

                def blocked(name, headers, body):
                    nonlocal active
                    active = name
                    count = upstream.requests.qsize()
                    fixed(name, request(port, "GET", "/mock/v1/models?fixture=1", headers), 401, body, False)
                    if upstream.requests.qsize() != count:
                        record(name, {"unexpected_forward": True}, {"unexpected_forward": False})

                blocked("caller absent credential", {}, b"unauthorized\n")
                blocked("caller invalid credential", {"Authorization": "Bearer invalid-fixture"},
                        b"unauthorized or route not granted\n")
                blocked("admin key cannot forward", {"Authorization": "Bearer " + probe.token.read_text()},
                        b"unauthorized\n")

                def grants(name, routes):
                    nonlocal active
                    active = name
                    fixed(name, api("PUT", "callers", {"id": caller_id, "grants": routes, "revision": revision},
                          authenticated=True), 200, b'{"ok":true}\n')

                grants("remove grant", [])
                blocked("removed grant denied", auth, b"unauthorized or route not granted\n")
                grants("restore grant", ["mock"])
                active = "authenticated HTTP"
                code, headers, body = request(port, "GET", "/mock/v1/models?fixture=1", auth)
                record("authenticated HTTP", {"status": code, "body_exact": body == HTTP_BODY,
                                              "content_type_exact": headers.get("Content-Type") == "application/json",
                                              "cookie_stripped": headers.get("Set-Cookie") is None},
                       {"status": 207, "body_exact": True, "content_type_exact": True, "cookie_stripped": True})
                active = "authenticated gated SSE"
                conn = http.client.HTTPConnection("127.0.0.1", port, timeout=TIMEOUT)
                try:
                    conn.request("POST", "/mock/v1/chat/completions?stream=1", REQUEST_BODY,
                                 auth | {"Content-Type": "application/json"})
                    res = conn.getresponse()
                    # Reading exactly FIRST must succeed while the upstream is
                    # blocked at the gate. A buffering proxy times out here.
                    first = res.read(len(FIRST))
                    gated = upstream.first_sent.wait(TIMEOUT) and not upstream.release.is_set() and not upstream.second_sent.is_set()
                    upstream.release.set()
                    rest = res.read()
                    record("authenticated gated SSE", {"status": res.status, "first_exact": first == FIRST,
                           "first_before_release": gated, "rest_exact": rest == SECOND,
                           "content_type_exact": res.getheader("Content-Type") == "text/event-stream", "cookie_stripped": res.getheader("Set-Cookie") is None},
                           {"status": 200, "first_exact": True, "first_before_release": True, "rest_exact": True,
                            "content_type_exact": True, "cookie_stripped": True})
                except (OSError, http.client.HTTPException) as exc:
                    record("authenticated gated SSE", {"failure_type": type(exc).__name__},
                           {"complete_gated_stream": True})
                finally:
                    upstream.release.set()
                    conn.close()
                fixed("revoke caller", api("DELETE", "callers", {"id": caller_id}, authenticated=True), 200, b'{"ok":true}\n')
                blocked("revoked caller denied", auth, b"unauthorized or route not granted\n")
                fixed("revoked caller cannot be regranted", api("PUT", "callers",
                      {"id": caller_id, "grants": ["mock"]}, authenticated=True), 400,
                      b'{"error":"revoked clients cannot receive new grants; create a new client"}\n')
                fixed("logout requires CSRF", api("POST", "logout", {}, {"X-CSRF-Token": ""}, True), 403, csrf_error)
                active = "logout"
                code, headers, body = api("POST", "logout", {}, authenticated=True)
                cleared = http.cookies.SimpleCookie()
                cleared.load(headers.get("Set-Cookie", ""))
                c = cleared.get("tlp_admin")
                record("logout", {"status": code, "body_exact": body == b'{"ok":true}\n',
                                  "cookie_cleared": c is not None and c.value == "" and c["max-age"] == "0"},
                       {"status": 200, "body_exact": True, "cookie_cleared": True})
                fixed("logged out session denied", api("GET", "state", authenticated=True), 401, denied)
                auth_status("auth status after logout")
                observed_requests = []
                while not upstream.requests.empty():
                    observed_requests.append(upstream.requests.get_nowait())
                record("upstream accounting", {"requests": observed_requests, "no_fixture_errors": upstream.errors.empty()},
                       {"requests": [
                           {"get_models": True, "post_stream": False, "vendor_only": True,
                            "caller_absent": True, "body_exact": True, "query_exact": True},
                           {"get_models": False, "post_stream": True, "vendor_only": True,
                            "caller_absent": True, "body_exact": True, "query_exact": True}], "no_fixture_errors": True})
                active = "graceful shutdown"
                record("graceful shutdown", proc.stop(), 0)
        except (ScenarioFailure, OSError, ValueError, KeyError, TypeError, AttributeError,
                http.client.HTTPException, http.cookies.CookieError, subprocess.SubprocessError, queue.Empty) as exc:
            if active not in results:
                results[active] = {"observed": {"failure_type": type(exc).__name__}, "ok": False}
            elif not isinstance(exc, ScenarioFailure):
                results[active] = {"observed": {"failure_type": type(exc).__name__}, "ok": False}
            # No exception strings: HTTP/JSON/subprocess errors may contain data.
        for name in CASES:
            results.setdefault(name, {"observed": {"not_reached": True}, "ok": False})
        return results


def digest(binary):
    with binary.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def main():
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        go, rust = (Path(arg).resolve(strict=True) for arg in sys.argv[1:])
        hashes = {"go_sha256": digest(go), "rust_sha256": digest(rust)}
        with tempfile.TemporaryDirectory(prefix="term-llm-token-parity-") as temporary:
            root = Path(temporary)
            oracle = run(go, root / "go")
            control = run(go, root / "go-control")
            control_ok = all(oracle[name]["ok"] and control[name]["ok"] and
                             oracle[name]["observed"] == control[name]["observed"] for name in CASES)
            candidate = run(rust, root / "rust") if control_ok else None
        rows = [{"case": name, "go": oracle[name], "go_control": control[name],
                 "rust": candidate[name] if candidate is not None else {"not_run": "invalid oracle/control"},
                 "match": candidate is not None and candidate[name]["ok"] and
                 candidate[name]["observed"] == oracle[name]["observed"]} for name in CASES]
        print(json.dumps({**hashes, "oracle_control_valid": control_ok,
                          "coverage": "token sessions, CSRF, caller issue/update/revoke, HTTP and gated SSE",
                          "not_covered": ["persistence/restart", "real passkeys", "request ledger", "other transports"],
                          "results": rows}, indent=2))
        if not control_ok:
            return 2
        return 0 if all(row["match"] for row in rows) else 1
    except (OSError, ValueError, subprocess.SubprocessError):
        print("invalid parity run (input or harness failure; details suppressed)", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
