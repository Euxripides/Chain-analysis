#!/usr/bin/env python3
"""Isolated readiness stand-in for the API deploy negative calibration.

This process NEVER touches production: it binds a loopback port inside a temp tree and
serves only /health/ready, /health/live and one anonymous business probe. It exists so the
calibration can drive s7-deploy-api-v4.sh through failure modes that must fail closed
without ever injecting a fault into the real user service.

Modes (NEGCAL_MODE):
  ready    - 200 + JSON contract, all four dependencies ok (positive control)
  never    - always 503, never ready
  dep_bad  - 200 + JSON but clickhouse is down (dependency not ready)
  html     - 200 text/html body (the nginx 8448 "false ready" shape)
"""
import json
import os
import signal
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

MODE = os.environ.get("NEGCAL_MODE", "ready")
DELAY = float(os.environ.get("NEGCAL_DELAY", "0"))
PORT = int(os.environ.get("NEGCAL_PORT", "18099"))

READY_OK = {"status": "ready", "checks": {"postgres": "ok", "clickhouse": "ok", "nats": "ok", "minio": "ok"}}
READY_DEP_BAD = {"status": "ready", "checks": {"postgres": "ok", "clickhouse": "down", "nats": "ok", "minio": "ok"}}
NOT_READY = {"status": "not_ready", "checks": {"postgres": "ok", "clickhouse": "ok", "nats": "ok", "minio": "ok"}}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        return

    def _send(self, code, body, ctype):
        raw = body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/health/ready":
            if MODE == "never":
                return self._send(503, json.dumps(NOT_READY), "application/json")
            if MODE == "html":
                return self._send(200, "<!doctype html><html><body>vite</body></html>", "text/html")
            if MODE == "dep_bad":
                return self._send(200, json.dumps(READY_DEP_BAD), "application/json")
            return self._send(200, json.dumps(READY_OK), "application/json")
        if path == "/health/live":
            return self._send(200, json.dumps({"status": "ok"}), "application/json")
        if path == "/api/v1/download-jobs":
            return self._send(401, json.dumps({"code": "unauthenticated", "message": "auth required"}), "application/json")
        return self._send(404, json.dumps({"code": "not_found"}), "application/json")


def main():
    if DELAY > 0:
        time.sleep(DELAY)
    server = HTTPServer(("127.0.0.1", PORT), Handler)

    def stop(*_args):
        # os._exit, NOT server.shutdown(): shutdown() must be called from another thread
        # than serve_forever(), otherwise the handler deadlocks in the main thread and the
        # process keeps the listening socket bound while serving nothing.
        os._exit(0)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    server.serve_forever()


if __name__ == "__main__":
    main()
