#!/usr/bin/env python3
"""First black-box parity gate. Exit 1 for a difference, 2 for an invalid oracle/run.

Run with only loopback networking enabled. Usage: parity_test.py GO_BINARY RUST_BUNDLE
This is a starter suite, not the full release contract (see PARITY.md).
"""

import contextlib
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import queue
import re
import secrets
import subprocess
import sys
import tempfile
import threading


CLI = ("serve", "proxy")
TIMEOUT = 5
READY = re.compile(rb"(?:Proxy ready .* |listening )http://(?:localhost|127\.0\.0\.1):(\d+)\r?\n")


class ReadinessMock(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        self.send_response(200 if self.path == "/healthz" else 404)
        self.send_header("Content-Length", "0")
        self.end_headers()


def request(port, method, path):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=TIMEOUT)
    try:
        conn.request(method, path)
        res = conn.getresponse()
        return res.status, res.getheader("Cache-Control"), res.read()
    finally:
        conn.close()


def private_token(path):
    with open(path, "x", opener=lambda name, flags: os.open(name, flags, 0o600)) as out:
        out.write(secrets.token_hex(32))


class Probe:
    def __init__(self, binary, home):
        home.mkdir(mode=0o700)
        self.binary = binary
        self.home = home
        self.token = home / "admin-token"
        private_token(self.token)
        self.vendor = home / "vendor-token"
        private_token(self.vendor)
        self.env = {"PATH": os.defpath, "LANG": "C.UTF-8", "HOME": str(home),
                    "TERM_LLM_OFFLINE": "1"}
        for key in ("XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "TMPDIR"):
            self.env[key] = str(home / key.lower())
            Path(self.env[key]).mkdir(mode=0o700)

    def cli(self, *args, **env):
        return subprocess.run([self.binary, *args], cwd=self.home, env=self.env | env,
                              stdin=subprocess.DEVNULL, capture_output=True, timeout=TIMEOUT)

    def config(self, port):
        """An ordinary operator YAML config (JSON subset), identical in both runs."""
        origin = f"http://127.0.0.1:{port}"
        cfg = self.home / "proxy.json"
        cfg.write_text(json.dumps({
            "secrets": {"mock-key": {"file": str(self.vendor), "origin": origin}},
            "routes": [{"name": "mock", "local_prefix": "/mock/", "upstream": origin + "/backend",
                        "enabled": True, "allow_insecure_http": True,
                        "caller_auth": {"header": "Authorization", "prefix": "Bearer "},
                        "set_headers": {"Authorization": {"secret": "mock-key", "prefix": "Bearer "}},
                        "allow": ["GET /v1/models"]}],
        }), encoding="utf-8")
        return cfg

    def startup(self, *extra):
        """Return only status and selected health observables; never emit logs/secrets."""
        with subprocess.Popen([self.binary, *CLI, "--port", "0", *extra], cwd=self.home,
                              env=self.env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                              stderr=subprocess.DEVNULL) as proc:
            def read_port():
                for line in proc.stdout:
                    match = READY.fullmatch(line)
                    if match:
                        ready.put(int(match[1]))
                        return
                ready.put(None)

            ready = queue.Queue(maxsize=1)
            reader = threading.Thread(target=read_port, daemon=True)
            reader.start()
            try:
                port = ready.get(timeout=TIMEOUT)
                if port is None:
                    return {"started": False, "exit": proc.wait(timeout=TIMEOUT)}
                code, cache, body = request(port, "GET", "/healthz")
                proc.terminate()
                return {"started": True, "healthy": (code, cache, body) == (200, "no-store", b"ok\n"),
                        "exit": proc.wait(timeout=TIMEOUT)}
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait(timeout=TIMEOUT)
                reader.join(TIMEOUT)


def compare(go, rust, port):
    results = []

    def check(name, expected, probe):
        baseline = probe(go)
        if baseline != expected:
            raise ValueError(f"oracle did not meet expectation for {name}: {baseline!r}")
        candidate = probe(rust)
        results.append({"case": name, "go": baseline, "rust": candidate, "match": baseline == candidate})

    def health(p, host="127.0.0.1", **env):
        return p.cli(*CLI, "health", "--host", host, "--port", str(port), **env)

    def usage_flags(p):
        help_text = p.cli(*CLI, "--help")
        expect = {b"--config", b"--port", b"--state-dir", b"--auth", b"--admin-token-file",
                  b"--passkey-bootstrap-token-file", b"--passkey-recovery-token-file", b"--public-url",
                  b"--request-timeout", b"--retention-days", b"--debug-payload-dir"}
        if help_text.returncode != 0:
            raise ValueError("CLI help failed")
        return [flag.decode() for flag in sorted(expect - set(re.findall(rb"--[a-z-]+", help_text.stdout)))]

    def quiet_health(p):
        result = health(p)
        return not (result.stdout or result.stderr)

    check("health reaches loopback readiness (exit)", 0, lambda p: health(p).returncode)
    check("health success is silent", True, quiet_health)
    check("health accepts localhost (exit)", 0, lambda p: health(p, "localhost").returncode)
    check("health refuses inherited profiling before running", True,
          lambda p: health(p, TERM_LLM_PPROF="0").returncode != 0)
    check("documented operator flags (missing flags)", [], usage_flags)
    ready = {"started": True, "healthy": True, "exit": 0}
    check("default passkey startup with no operator config", ready, lambda p: p.startup())
    check("token mode with private admin file and origin-bound operator config", ready,
          lambda p: p.startup("--state-dir", str(p.home / "operator-state"), "--auth", "token",
                              "--admin-token-file", str(p.token), "--config", str(p.config(port))))
    return results


def main():
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        go, rust = (Path(arg).resolve(strict=True) for arg in sys.argv[1:])
        with tempfile.TemporaryDirectory(prefix="term-llm-parity-") as tmp:
            home = Path(tmp)
            with http.server.ThreadingHTTPServer(("127.0.0.1", 0), ReadinessMock) as server:
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                try:
                    results = compare(Probe(go, home / "go"), Probe(rust, home / "rust"), server.server_port)
                finally:
                    server.shutdown()
                    thread.join(TIMEOUT)
        report = {"coverage": "CLI and startup starter cases only", "results": results}
        for label, binary in (("go_sha256", go), ("rust_sha256", rust)):
            with binary.open("rb") as handle:
                report[label] = hashlib.file_digest(handle, "sha256").hexdigest()
        print(json.dumps(report, indent=2))
        return 0 if all(case["match"] for case in results) else 1
    except (OSError, ValueError, subprocess.SubprocessError, queue.Empty) as exc:
        print(f"invalid parity run: {exc if type(exc) is ValueError else type(exc).__name__}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
