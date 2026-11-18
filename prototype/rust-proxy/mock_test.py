#!/usr/bin/env python3
"""Hermetic packaged Rust proxy smoke test (stdlib only, no live providers).

Usage: python mock_test.py /path/to/term-llm
Runs only the supplied executable, copied without siblings into a private sandbox.
WebSocket support is deliberately not required by this first-version contract.
"""

import contextlib
import http.client
import http.server
import json
import os
from pathlib import Path
import queue
import re
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time


TIMEOUT = 5
START_TIMEOUT = 15
FIRST = b'data: {"part":1}\n\n'
SECOND = b'data: {"part":2}\n\ndata: [DONE]\n\n'


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def receipt(message):
    print("PASS " + message, flush=True)


class Fixture(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), Handler)
        self.requests = queue.Queue()
        self.errors = queue.Queue()
        self.release_second = threading.Event()
        self.first_sent = threading.Event()
        self.second_sent = threading.Event()

    def handle_error(self, request, client_address):
        # Do not dump request headers (even synthetic credentials) in tracebacks.
        self.errors.put("mock handler raised an exception")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        self.connection.settimeout(TIMEOUT)
        # Never leave a pooled upstream socket alive for the absent-upstream case.
        self.close_connection = True
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.server.requests.put((self.command, self.path, self.headers, body))
        if self.path == "/backend/v1/redirect":
            self.send_response(302)
            self.send_header("Location", "/backend/redirect-target")
            self.send_header("Content-Length", "0")
            self.send_header("Connection", "close")
            self.end_headers()
            return
        if self.path == "/backend/v1/chat/completions?stream=1":
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Set-Cookie", "synthetic=discard")
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(FIRST)
            self.wfile.flush()
            self.server.first_sent.set()
            if not self.server.release_second.wait(2 * TIMEOUT):
                self.server.errors.put("SSE client never released second event")
                return
            self.wfile.write(SECOND)
            self.wfile.flush()
            self.server.second_sent.set()
            self.close_connection = True
            return
        payload = b'{"mock":"byte-for-byte"}\n'
        self.send_response(207)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-ID", "synthetic-request-id")
        self.send_header("Set-Cookie", "synthetic=discard")
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(payload)


@contextlib.contextmanager
def mock_upstream():
    server = Fixture()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.release_second.set()
        server.shutdown()
        server.server_close()
        thread.join(TIMEOUT)
        require(not thread.is_alive(), "mock server failed to stop")


class Proxy:
    def __init__(self, binary, config, env, extra=()):
        self.command = [str(binary), "serve", "proxy", "--config", str(config),
                        "--port", "0", *extra]
        self.env = env
        self.cwd = binary.parent
        self.proc = None
        self.lines = queue.Queue()
        self.readers = []
        self.port = None

    def drain(self, stream, stdout):
        try:
            for line in iter(stream.readline, b""):
                if stdout:
                    # Only retain the public readiness marker, not arbitrary logs.
                    match = re.fullmatch(rb"Proxy ready .* http://127\.0\.0\.1:([0-9]+)\r?\n", line)
                    if match:
                        self.lines.put(int(match.group(1)))
        finally:
            stream.close()

    def __enter__(self):
        self.proc = subprocess.Popen(self.command, cwd=self.cwd, env=self.env,
                                     stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                     stderr=subprocess.PIPE)
        try:
            for stream, stdout in ((self.proc.stdout, True), (self.proc.stderr, False)):
                thread = threading.Thread(target=self.drain, args=(stream, stdout), daemon=True)
                thread.start()
                self.readers.append(thread)
            deadline = time.monotonic() + START_TIMEOUT
            while time.monotonic() < deadline:
                require(self.proc.poll() is None, "proxy exited before readiness marker")
                try:
                    self.port = self.lines.get(timeout=0.1)
                    require(0 < self.port < 65536, "invalid readiness port")
                    return self
                except queue.Empty:
                    pass
            raise AssertionError("no flushed stdout readiness marker within deadline")
        except BaseException:
            self.cleanup()
            raise

    def graceful_stop(self):
        require(self.proc.poll() is None, "proxy exited before SIGTERM")
        self.proc.send_signal(signal.SIGTERM)
        try:
            code = self.proc.wait(timeout=TIMEOUT)
        except subprocess.TimeoutExpired:
            raise AssertionError("proxy failed to stop gracefully within deadline") from None
        require(code == 0, "SIGTERM must produce graceful exit status 0")
        receipt("SIGTERM gracefully stopped the tested proxy process (exit 0)")

    def cleanup(self):
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=TIMEOUT)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait(timeout=TIMEOUT)
        for thread in self.readers:
            thread.join(TIMEOUT)

    def __exit__(self, *args):
        self.cleanup()


def cli(binary, env, *args):
    try:
        result = subprocess.run([str(binary), *args], cwd=binary.parent, env=env,
                                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=START_TIMEOUT)
    except subprocess.TimeoutExpired:
        raise AssertionError("CLI command exceeded deadline") from None
    require(result.returncode == 0, "CLI command failed: " + " ".join(args))
    return result.stdout + result.stderr


def request(port, method, path, headers=None, body=None):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=TIMEOUT)
    try:
        conn.request(method, path, body=body, headers=headers or {})
        response = conn.getresponse()
        return response.status, response.headers, response.read()
    finally:
        conn.close()


def next_request(server):
    try:
        return server.requests.get(timeout=TIMEOUT)
    except queue.Empty:
        raise AssertionError("expected upstream request was not received") from None


def check_no_upstream(server):
    # Requests under test have completed, so any forwarded request is already queued.
    require(server.requests.empty(), "blocked request unexpectedly reached upstream")
    require(server.errors.empty(), "mock upstream encountered an error")


def run(binary_source):
    require(binary_source.is_file(), "binary path must name a regular file")
    with tempfile.TemporaryDirectory(prefix="term-llm-rust-mock-") as temporary:
        root = Path(temporary)
        bindir = root / "bin"
        bindir.mkdir()
        binary = bindir / "term-llm"
        shutil.copy2(binary_source, binary)
        binary.chmod(0o700)
        require(list(bindir.iterdir()) == [binary], "binary directory is not isolated")
        # Do not inherit user config, real tokens, proxy variables or loader overrides.
        env = {"PATH": os.defpath, "LANG": "C", "HOME": str(root / "home"),
               "XDG_CONFIG_HOME": str(root / "config"),
               "XDG_CACHE_HOME": str(root / "cache"),
               "XDG_DATA_HOME": str(root / "data"),
               "XDG_STATE_HOME": str(root / "state"), "TMPDIR": str(root / "tmp"),
               "TERM_LLM_OFFLINE": "1"}
        for key in ("HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
                    "XDG_STATE_HOME", "TMPDIR"):
            Path(env[key]).mkdir()
        upstream_token = secrets.token_hex(24)
        token_file = root / "upstream-token"
        with open(token_file, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as handle:
            handle.write(upstream_token)
        require(token_file.stat().st_mode & 0o777 == 0o600, "token file is not mode 0600")
        require(b"Rust" in cli(binary, env, "serve", "proxy", "--help"),
                "serve proxy help does not identify Rust")
        require(b"term-llm" in cli(binary, env, "--version"), "non-proxy CLI version missing")
        receipt("isolated single-binary CLI: normal version and Rust proxy help")
        admin_token = secrets.token_hex(32)
        admin_file = root / "admin-token"
        with open(admin_file, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as handle:
            handle.write(admin_token)

        def issue(port):
            headers = {"Origin": f"http://127.0.0.1:{port}", "Content-Type": "application/json"}
            code, reply, body = request(port, "POST", "/_proxy/api/login", headers,
                                        json.dumps({"token": admin_token}).encode())
            require(code == 200, "token-mode admin login failed")
            headers.update({"Cookie": reply["Set-Cookie"].split(";", 1)[0], "X-CSRF-Token": json.loads(body)["csrf"]})
            code, _, body = request(port, "POST", "/_proxy/api/callers", headers,
                                    b'{"name":"smoke","grants":["mock"]}')
            require(code == 201, "caller issuance failed")
            return {"Authorization": "Bearer " + json.loads(body)["token"]}

        def admin_args(name):
            return ("--auth", "token", "--admin-token-file", str(admin_file), "--state-dir", str(root / name))
        config = root / "proxy.json"
        with mock_upstream() as server:
            config.write_text(json.dumps({
                "secrets": {"upstream": {"file": str(token_file), "origin": f"http://127.0.0.1:{server.server_port}"}},
                "routes": [{"name": "mock", "local_prefix": "/mock/",
                            "upstream": "http://127.0.0.1:%d/backend" % server.server_port,
                            "enabled": True, "allow_insecure_http": True, "redirects": "refuse",
                            "caller_auth": {"header": "Authorization", "prefix": "Bearer "},
                            "set_headers": {"Authorization": {"secret": "upstream", "prefix": "Bearer "}},
                            "allow": ["POST /v1/chat/completions", "GET /v1/models",
                                      "GET /v1/redirect", "GET /v1/realtime"]}]
            }), encoding="utf-8")
            with Proxy(binary, config, env, admin_args("proxy-state")) as proxy:
                require("memfd:term-llm-rust-proxy" in os.readlink(f"/proc/{proxy.proc.pid}/exe"),
                        "Rust did not replace the Go process image")
                receipt("same PID is executing the embedded Rust process image")
                auth = issue(proxy.port)
                require(request(proxy.port, "GET", "/healthz")[0] == 200, "GET /healthz failed")
                cli(binary, env, "serve", "proxy", "health", "--port", str(proxy.port))
                receipt("port-zero readiness, /healthz and CLI health")
                for headers in ({}, {"Authorization": "Bearer synthetic-wrong"}):
                    require(request(proxy.port, "GET", "/mock/v1/models", headers)[0] == 401,
                            "missing/wrong caller token must return 401")
                for method, path in (("GET", "/mock/v1/forbidden"),
                                     ("POST", "/mock/v1/models")):
                    require(request(proxy.port, method, path, auth)[0] == 403,
                            "route method/path allowlist must return 403")
                require(request(proxy.port, "GET", "/unknown/v1/models", auth)[0] == 404,
                        "unknown route must return 404")
                check_no_upstream(server)
                receipt("401 caller auth, 403 method/path allowlist, 404 unknown route")

                headers = dict(auth, **{"Content-Type": "application/json", "Accept": "application/json",
                                       "X-Term-LLM-Proxy-Private": "synthetic-discard", "Cookie": "synthetic=discard",
                                       "Forwarded": "for=192.0.2.1;proto=https",
                                       "X-Forwarded-For": "192.0.2.2", "X-Forwarded-Host": "invalid.example",
                                       "X-Forwarded-Proto": "https"})
                body = b'{"messages":[],"marker":"exact bytes"}\n'
                path = "/mock/v1/chat/completions?q=a%2Fb&repeat=1&repeat=2&empty="
                status, reply_headers, reply = request(proxy.port, "POST", path, headers, body)
                require(status == 207 and reply == b'{"mock":"byte-for-byte"}\n',
                        "upstream status/body were not transparent")
                require(reply_headers.get("X-Request-ID") == "synthetic-request-id", "response header lost")
                require(reply_headers.get("Set-Cookie") is None, "upstream Set-Cookie leaked")
                method, target, forwarded, actual_body = next_request(server)
                require(method == "POST" and target == path.replace("/mock/", "/backend/", 1)
                        and actual_body == body, "method/path/query/body were not transparent")
                require(forwarded.get_all("Authorization") == ["Bearer " + upstream_token],
                        "upstream auth must replace caller auth exactly once")
                require(forwarded.get("Accept") == "application/json" and
                        forwarded.get("Content-Type") == "application/json", "request header lost")
                for name in forwarded.keys():
                    require(name.lower() not in ("cookie", "x-term-llm-proxy-private", "forwarded") and
                            not name.lower().startswith("x-forwarded-"),
                            "caller credential/forwarding header leaked")
                receipt("transparent request/query/status/body; credential and Set-Cookie stripping")

                require(request(proxy.port, "GET", "/mock/v1/models", auth)[0] == 207,
                        "allowed GET was not forwarded")
                require(next_request(server)[:2] == ("GET", "/backend/v1/models"), "GET path lost")
                require(request(proxy.port, "GET", "/mock/v1/redirect", auth)[0] == 502,
                        "redirect with Location must return 502")
                require(next_request(server)[:2] == ("GET", "/backend/v1/redirect"), "redirect path lost")
                check_no_upstream(server)
                receipt("allowed GET and redirect rejected without following Location")

                conn = http.client.HTTPConnection("127.0.0.1", proxy.port, timeout=TIMEOUT)
                try:
                    conn.request("POST", "/mock/v1/chat/completions?stream=1", body=b"{}", headers=auth)
                    response = conn.getresponse()
                    require(response.status == 200, "SSE status mismatch")
                    require(response.headers.get_content_type() == "text/event-stream", "SSE content type lost")
                    require(response.headers.get("Set-Cookie") is None, "SSE Set-Cookie leaked")
                    require(response.read(len(FIRST)) == FIRST, "SSE first event not delivered promptly")
                    require(server.first_sent.wait(TIMEOUT) and not server.second_sent.is_set()
                            and not server.release_second.is_set(), "SSE gate invariant failed")
                    # Only the client, after observing event one, permits event two.
                    server.release_second.set()
                    require(response.read() == SECOND, "SSE remainder changed or truncated")
                    require(server.second_sent.wait(TIMEOUT), "mock did not finish SSE")
                    require(next_request(server)[:2] == ("POST", "/backend/v1/chat/completions?stream=1"),
                            "SSE request target changed")
                finally:
                    server.release_second.set()
                    conn.close()
                check_no_upstream(server)
                receipt("SSE first event arrived before the mock was permitted to send event two")

                with Proxy(binary, config, env, (*admin_args("limited-state"), "--max-body-bytes", "16")) as limited:
                    limited_auth = issue(limited.port)
                    require(request(limited.port, "POST", "/mock/v1/chat/completions", limited_auth, b"x" * 17)[0] == 413,
                            "known Content-Length over --max-body-bytes must return 413")
                    check_no_upstream(server)
                    limited.graceful_stop()
                receipt("known-length body limit override returns 413 without forwarding")

                # Close the real listener, not a guessed unused port. This also makes
                # the failure deterministic even if the proxy pooled a connection.
                server.shutdown()
                server.server_close()
                require(request(proxy.port, "GET", "/mock/v1/models", auth)[0] == 502,
                        "absent upstream must return 502")
                receipt("absent upstream returns 502")
                proxy.graceful_stop()
        require(list(bindir.iterdir()) == [binary], "unexpected sidecar created beside binary")
        receipt("single copied executable completed all checks without a sidecar")


def main():
    if len(sys.argv) != 2:
        print("usage: python mock_test.py /path/to/term-llm", file=sys.stderr)
        return 2
    try:
        run(Path(sys.argv[1]).resolve(strict=True))
    except Exception as error:
        # Assertions deliberately contain no header values or process log output.
        detail = str(error) if isinstance(error, AssertionError) else type(error).__name__
        print("FAIL " + detail, file=sys.stderr, flush=True)
        return 1
    print("PASS all packaged Rust proxy mock checks (WebSocket not required)", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
