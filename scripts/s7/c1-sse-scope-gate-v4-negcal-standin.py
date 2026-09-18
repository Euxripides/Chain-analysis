#!/usr/bin/env python3
"""Old-behaviour SSE stand-in for the c1-sse-scope-gate-v4 negative calibration.

It reproduces EXACTLY the two measured defects of the pre-fix server, on an isolated loopback
port, with no database and no production process involved:

  C1-SSE-OLD-SCOPE-OPEN        a subscription opened under workspace A is never closed when the
                               same session switches workspace, and it keeps receiving frames
                               (here: a heartbeat every second)
  C1-SSE-NO-SUBSCRIPTION-AUDIT it writes no audit row at all (it has no database access)

Request-id headers are emitted so the calibration can run the same correlation logic the real
gate uses. Nothing about this process is real: it exists only to prove the gate's predicates
report a failure when the fix is absent.
"""
import json
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18120
STATE = {"workspace": "A", "switches": 0}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        return

    def _rid(self):
        return str(uuid.uuid4())

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        rid = self._rid()
        if self.path == "/api/auth/v1/session/workspace":
            STATE["workspace"] = "B"
            STATE["switches"] += 1
            body = json.dumps({"success": True, "data": {"state": "switched"}}).encode()
            self.send_response(200)
        else:
            body = json.dumps({"success": True, "data": {"state": "ok"}}).encode()
            self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-Id", rid)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        rid = self._rid()
        if self.path.endswith("/events"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("X-Request-Id", rid)
            self.send_header("Connection", "close")
            self.end_headers()
            try:
                self.wfile.write(b"event: snapshot\ndata: {\"status\":\"RUNNING\"}\n\n")
                self.wfile.flush()
                while True:
                    time.sleep(1)
                    self.wfile.write(b": ping\n\n")
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, OSError):
                return
            return
        body = json.dumps({"success": True, "data": {"id": "old-behaviour-job"}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-Id", rid)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    server = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print("standin_listening port=%d workspace=A" % PORT, flush=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        server.shutdown()


if __name__ == "__main__":
    main()
