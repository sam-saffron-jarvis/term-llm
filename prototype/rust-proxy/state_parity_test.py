#!/usr/bin/env python3
"""Quiesced Go state -> candidate -> candidate restart -> Go rollback contract.

Usage: state_parity_test.py PINNED_GO_CLI CANDIDATE_CLI
The Go artifact must be built from upstream 0821cc18f9b6bc69cdf6014c69eb6b837863ceee.
Run only in the parent's leased, loopback-only sandbox with already-built real
executables. This script never builds, downloads, or uses a live provider.

First run the entire chain Go-vs-Go; only a green control permits the candidate
chain. Each chain creates operator-mode state through the Go CLI/admin HTTP API,
issues two scoped callers, sinks one by revocation, tests both, and stops Go.
Copy the entire quiesced private state tree (including any SQLite sidecars), not
an exported SQL fixture. On that copy, with the SAME config and credential files:
  * candidate must retain the active grant and revoked key, including scope;
  * candidate revokes the original active caller and issues a replacement;
  * candidate restart must retain both revocations and the replacement grant;
  * Go rollback on that SAME mutated copy must retain the identical result.
Fresh admin logins after each restart avoid assuming session portability. Caller
IDs, creation times and grant relationships are compared privately across stages;
only fixed predicates/statuses and executable/script SHA256s enter the report.
No state, token, credential, raw response, exception text or logs are exported.
Temporary homes/state/config/credentials are private and removed on completion.

Exit 0: green control and candidate; 1: red candidate (including missing binary);
2: invalid control or harness/transport error. Missing candidate and dependency
skips have distinct statuses, never masquerade as passed cases or runtime errors.
Readiness without state support is NOT parity. This covers graceful restarts and
current pinned-Go state adoption/rollback, not crash recovery, historic schema
versions, passkeys, ledger/cost/retention, live providers or Rust-only ownership.
"""

import copy
import http.client
import http.cookies
import json
from pathlib import Path
import queue
import shutil
import subprocess
import sys
import tempfile

from token_parity_test import (
    API, HTTP_BODY, Process, Probe, ScenarioFailure, digest, fixture, request, shape,
)


PINNED_GO = "0821cc18f9b6bc69cdf6014c69eb6b837863ceee"
STAGES = ("seed", "migration", "restart", "rollback")
STAGE_CASES = {
    "seed": ("startup", "login", "state", "issue active", "issue sunk", "revoke sunk",
             "metadata", "active allowed", "active scope denied", "sunk denied", "stop"),
    "migration": ("startup", "login", "state", "metadata", "active allowed",
                  "active scope denied", "sunk denied", "revoke active", "issue replacement",
                  "mutated metadata", "active denied", "replacement allowed",
                  "replacement scope denied", "stop"),
    "restart": ("startup", "login", "state", "metadata", "active denied", "sunk denied",
                "replacement allowed", "replacement scope denied", "stop"),
    "rollback": ("startup", "login", "state", "metadata", "active denied", "sunk denied",
                 "replacement allowed", "replacement scope denied", "stop"),
}
CASES = tuple(["seed/" + name for name in STAGE_CASES["seed"]] + ["quiesced copy"] +
              [stage + "/" + name for stage in STAGES[1:] for name in STAGE_CASES[stage]])
ERRORS = (OSError, ValueError, KeyError, TypeError, AttributeError,
          http.client.HTTPException, http.cookies.CookieError,
          subprocess.SubprocessError, queue.Empty)


def skipped(reason):
    return {"status": "skipped", "reason": reason}


class Chain:
    def __init__(self, go, candidate, home):
        self.go = go
        self.candidate = candidate
        self.home = home
        self.results = {}
        self.active = CASES[0]
        self.callers = {}
        self.session = {}
        self.revision = None

    def check(self, name, observed, expected):
        self.active = name
        ok = observed == expected
        self.results[name] = {"status": "pass" if ok else "fail",
                              "observed": observed, "expected": expected}
        # Everything is a prerequisite: never manufacture downstream differences.
        if not ok:
            raise ScenarioFailure()

    def api(self, method, path, value=None):
        headers = {"Content-Type": "application/json", **self.session}
        if method != "GET":
            headers["Origin"] = "http://127.0.0.1:%d" % self.port
        return request(self.port, method, API + path, headers,
                       None if value is None else json.dumps(value).encode())

    def login(self, name):
        self.active = name
        self.session = {}
        code, headers, body = self.api("POST", "login", {"token": self.probe.token.read_text()})
        data = json.loads(body)
        cookies = http.cookies.SimpleCookie()
        cookies.load(headers.get("Set-Cookie", ""))
        cookie = cookies.get("tlp_admin")
        csrf = data.get("csrf")
        self.check(name, {"status": code, "csrf_shape": shape(csrf, "csrf_", 64),
                         "cookie_shape": cookie is not None and shape(cookie.value, "admin_", 64),
                         "no_store": headers.get("Cache-Control") == "no-store"},
                   {"status": 200, "csrf_shape": True, "cookie_shape": True, "no_store": True})
        self.session = {"Cookie": "tlp_admin=" + cookie.value, "X-CSRF-Token": csrf}

    def state(self, name, empty=False):
        self.active = name
        code, headers, body = self.api("GET", "state")
        data = json.loads(body)
        revision = data.get("revision")
        self.check(name, {"status": code, "operator_token_mode":
                         data.get("mode") == "file" and data.get("auth") == "token",
                         "revision_valid": type(revision) is int and revision >= 1,
                         "csrf_relationship": data.get("csrf") == self.session["X-CSRF-Token"],
                         "no_store": headers.get("Cache-Control") == "no-store",
                         "callers_valid": data.get("callers") == [] if empty else
                         isinstance(data.get("callers"), list)},
                   {"status": 200, "operator_token_mode": True, "revision_valid": True,
                    "csrf_relationship": True, "no_store": True, "callers_valid": True})
        self.revision = revision
        return data

    def issue(self, name, label):
        self.active = name
        code, _, body = self.api("POST", "callers", {
            "name": label, "grants": ["mock"], "revision": self.revision})
        data = json.loads(body)
        caller = data.get("caller", {})
        token = data.get("token")
        self.check(name, {"status": code, "token_shape": shape(token, "sk-tlp-", 64),
                         "id_shape": shape(caller.get("id"), "c_", 24),
                         "grant_exact": caller.get("grants") == ["mock"],
                         "not_revoked": caller.get("revoked") is False,
                         "name_exact": caller.get("name") == label,
                         "created_valid": type(caller.get("created")) is int and caller["created"] > 0,
                         "distinct": all(token != c["token"] and caller.get("id") != c["id"]
                                         for c in self.callers.values())},
                   {"status": 201, "token_shape": True, "id_shape": True, "grant_exact": True,
                    "not_revoked": True, "name_exact": True, "created_valid": True, "distinct": True})
        self.callers[label] = {**caller, "token": token}

    def revoke(self, name, label):
        self.active = name
        code, _, body = self.api("DELETE", "callers", {"id": self.callers[label]["id"]})
        self.check(name, {"status": code, "body_exact": body == b'{"ok":true}\n'},
                   {"status": 200, "body_exact": True})
        self.callers[label]["revoked"] = True
        # Pinned Go retains grant metadata on revocation; authorization must still
        # deny the token. Preserve that distinction across adoption and rollback.

    def metadata(self, name):
        self.active = name
        code, _, body = self.api("GET", "state")
        listed = json.loads(body).get("callers", [])
        by_id = {c["id"]: c for c in listed}
        fields = ("id", "name", "created", "revoked", "grants", "created_by", "external_id")
        exact = len(listed) == len(self.callers) and all(
            saved["id"] in by_id and all(by_id[saved["id"]].get(k) == saved.get(k) for k in fields)
            for saved in self.callers.values())
        self.check(name, {"status": code, "persisted_metadata_exact": exact},
                   {"status": 200, "persisted_metadata_exact": True})

    def forward(self, name, label, allowed, scoped=False):
        self.active = name
        token = self.callers[label]["token"]
        self.upstream.caller_token = token
        path = "/other/v1/models?fixture=1" if scoped else "/mock/v1/models?fixture=1"
        code, _, body = request(self.port, "GET", path, {"Authorization": "Bearer " + token})
        captured = []
        while not self.upstream.requests.empty():
            captured.append(self.upstream.requests.get_nowait())
        wanted = [{"get_models": True, "post_stream": False, "vendor_only": True,
                   "caller_absent": True, "body_exact": True, "query_exact": True}] if allowed else []
        self.check(name, {"status": code, "body_exact": body == (
            HTTP_BODY if allowed else b"unauthorized or route not granted\n"),
            "upstream_exact": captured == wanted, "fixture_clean": self.upstream.errors.empty()},
            {"status": 207 if allowed else 401, "body_exact": True,
             "upstream_exact": True, "fixture_clean": True})

    def stage(self, stage, binary, state):
        self.active = stage + "/startup"
        probe = copy.copy(self.probe)
        probe.binary = binary
        proc = Process(probe, self.config)
        proc.command[proc.command.index("--state-dir") + 1] = str(state)
        with proc:
            try:
                self.port = proc.ready()
            except (ScenarioFailure, queue.Empty):
                self.check(self.active, {"ready": False}, {"ready": True})
            self.check(self.active, {"ready": True}, {"ready": True})
            self.login(stage + "/login")
            self.state(stage + "/state", empty=stage == "seed")
            if stage == "seed":
                self.issue(stage + "/issue active", "active")
                self.issue(stage + "/issue sunk", "sunk")
                self.revoke(stage + "/revoke sunk", "sunk")
            self.metadata(stage + "/metadata")
            original_live = stage in ("seed", "migration")
            self.forward(stage + ("/active allowed" if original_live else "/active denied"),
                         "active", original_live)
            if original_live:
                self.forward(stage + "/active scope denied", "active", False, scoped=True)
            self.forward(stage + "/sunk denied", "sunk", False)
            if stage == "migration":
                self.revoke(stage + "/revoke active", "active")
                self.issue(stage + "/issue replacement", "replacement")
                self.metadata(stage + "/mutated metadata")
                self.forward(stage + "/active denied", "active", False)
            if stage != "seed":
                self.forward(stage + "/replacement allowed", "replacement", True)
                self.forward(stage + "/replacement scope denied", "replacement", False, scoped=True)
            self.active = stage + "/stop"
            self.check(self.active, {"exit": proc.stop()}, {"exit": 0})

    def run(self):
        try:
            self.probe = Probe(self.go, self.home)
            self.probe.env.pop("TERM_LLM_RUST_PROXY_TOKEN", None)
            with fixture() as self.upstream:
                self.upstream.expected_vendor = self.probe.vendor.read_text()
                self.upstream.caller_token = "not-issued-yet"
                self.config = self.probe.config(self.upstream.server_port)
                cfg = json.loads(self.config.read_text())
                cfg["routes"].append({**cfg["routes"][0], "name": "other", "local_prefix": "/other/"})
                self.config.write_text(json.dumps(cfg), encoding="utf-8")
                config_bytes = self.config.read_bytes()
                original = self.home / "proxy-state"
                adopted = self.home / "adopted-state"
                self.stage("seed", self.go, original)
                self.active = "quiesced copy"
                # stage() has reaped the process and closed stdout before copying.
                shutil.copytree(original, adopted, symlinks=False)
                self.check(self.active, {"private_source": original.stat().st_mode & 0o777 == 0o700,
                                        "private_copy": adopted.stat().st_mode & 0o777 == 0o700,
                                        "nonempty": any(adopted.iterdir()),
                                        "config_unchanged": self.config.read_bytes() == config_bytes},
                           {"private_source": True, "private_copy": True,
                            "nonempty": True, "config_unchanged": True})
                for stage, binary in (("migration", self.candidate), ("restart", self.candidate),
                                      ("rollback", self.go)):
                    self.stage(stage, binary, adopted)
                    if self.config.read_bytes() != config_bytes:
                        # Do not silently accept a candidate rewriting the operator input.
                        self.results[stage + "/stop"] = {"status": "fail", "observed": {
                            "config_unchanged": False}, "expected": {"config_unchanged": True}}
                        raise ScenarioFailure()
        except ScenarioFailure:
            pass
        except ERRORS as exc:
            self.results[self.active] = {"status": "error", "failure_type": type(exc).__name__}
        finally:
            for name in CASES:
                self.results.setdefault(name, skipped("failed prerequisite"))
        return self.results


def green(results):
    return all(results[name]["status"] == "pass" for name in CASES)


def main():
    if len(sys.argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    report = {"contract": "quiesced-state migration/restart/rollback", "pinned_go_revision": PINNED_GO,
              "pin_verification": "caller supplies pinned artifact; SHA256 identifies actual bytes",
              "not_covered": ["crash recovery", "historic schema migration", "passkeys", "ledger/cost/retention",
                              "live providers", "Rust-only ownership"]}
    try:
        go = Path(sys.argv[1]).resolve(strict=True)
        candidate = Path(sys.argv[2]).resolve()
        report["go_sha256"] = digest(go)
        report["suite_sha256"] = digest(Path(__file__))
        report["helper_sha256"] = {name: digest(Path(__file__).with_name(name)) for name in
                                   ("token_parity_test.py", "parity_test.py")}
        candidate_exists = candidate.is_file()
        report["candidate_sha256"] = digest(candidate) if candidate_exists else None
        with tempfile.TemporaryDirectory(prefix="term-llm-state-parity-") as tmp:
            root = Path(tmp)
            # Private source/copy never leave this temporary tree; no fixture export.
            control = Chain(go, go, root / "control").run()
            control_ok = green(control)
            if not control_ok:
                tested = {name: skipped("invalid Go-vs-Go control") for name in CASES}
            elif not candidate_exists:
                tested = {name: skipped("missing candidate") for name in CASES}
                tested["migration/startup"] = {"status": "missing", "reason": "candidate binary absent"}
            else:
                tested = Chain(go, candidate, root / "candidate").run()
        has_error = any(row["status"] == "error" for row in tested.values())
        exit_code = 2 if not control_ok or has_error else (0 if green(tested) else 1)
        report.update({"oracle_control_valid": control_ok,
                       "candidate_input": "present" if candidate_exists else "missing",
                       "verdict": "green" if exit_code == 0 else "error" if exit_code == 2 else "red",
                       "results": [{"case": name, "go_control": control[name], "candidate": tested[name],
                                    "match": control[name]["status"] == tested[name]["status"] == "pass"
                                    and control[name]["observed"] == tested[name]["observed"]}
                                   for name in CASES]})
    except ERRORS as exc:
        exit_code = 2
        report.update({"verdict": "error", "failure_type": type(exc).__name__})
    print(json.dumps(report, indent=2))
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
