#!/usr/bin/env python3
"""Two unrelated local HTTP fixtures. Synthetic credentials only; Ctrl-C stops both."""
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def handler(base, header, credential):
    class Mock(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args):
            pass  # No request paths, credentials or bodies in logs.

        def do_GET(self):
            self.respond()

        def do_POST(self):
            self.respond()

        def respond(self):
            if self.headers.get(header) != credential:
                self.reply(401, b'{"error":"fixture credential mismatch"}')
                return
            if not self.path.startswith(base):
                self.reply(404, b'{"error":"fixture base mismatch"}')
                return
            length = int(self.headers.get("Content-Length", 0))
            if length:
                self.rfile.read(min(length, 1024 * 1024))
            path = self.path.split("?", 1)[0]
            if path.endswith("/events"):
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Connection", "close")
                self.end_headers()
                for n in range(5):
                    self.wfile.write(f'data: {{"step":{n}}}\n\n'.encode())
                    self.wfile.flush()
                    time.sleep(0.15)
                self.close_connection = True
            elif path.endswith("/binary"):
                self.reply(200, bytes(range(256)), "application/octet-stream")
            elif path.endswith("/error"):
                self.reply(429, b'{"error":"synthetic limit"}')
            else:
                self.reply(200, json.dumps({"fixture": base, "method": self.command,
                                          "path": path, "ok": True}).encode())

        def reply(self, status, body, content_type="application/json"):
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("X-Fixture-Request", "synthetic-request")
            self.end_headers()
            self.wfile.write(body)
    return Mock


if __name__ == "__main__":
    servers = [ThreadingHTTPServer(("127.0.0.1", 8091), handler("/alpha/", "X-Demo-A", "Bearer demo-alpha")),
               ThreadingHTTPServer(("127.0.0.1", 8092), handler("/unrelated/v2/", "X-Demo-B", "demo-beta"))]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    print("Mocks: http://127.0.0.1:8091/alpha/ (X-Demo-A: Bearer demo-alpha)", flush=True)
    print("       http://127.0.0.1:8092/unrelated/v2/ (X-Demo-B: demo-beta)", flush=True)
    print("Paths: anything, events, binary, error. Ctrl-C stops fixtures.", flush=True)
    try:
        while True:
            time.sleep(3600)
    except KeyboardInterrupt:
        for server in servers:
            server.shutdown()
            server.server_close()
