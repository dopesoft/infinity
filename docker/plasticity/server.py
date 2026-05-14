#!/usr/bin/env python3
"""Plasticity sidecar.

This service is intentionally metadata-first today. Core owns the control
plane and Trust gates; the sidecar owns heavyweight dataset/train/eval work
once a GPU or external runner is attached.
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


def response(status: str = "ready", **extra: Any) -> dict[str, Any]:
    body = {
        "service": "plasticity",
        "status": status,
        "mode": os.environ.get("PLASTICITY_MODE", "metadata"),
        "runner": os.environ.get("PLASTICITY_RUNNER", "none"),
    }
    body.update(extra)
    return body


class Handler(BaseHTTPRequestHandler):
    server_version = "infinity-plasticity/0.1"

    def do_GET(self) -> None:
        if self.path == "/health":
            self.write_json(200, {"ok": True})
            return
        if self.path == "/status":
            self.write_json(200, response())
            return
        self.write_json(404, {"error": "not found"})

    def do_POST(self) -> None:
        body = self.read_json()
        if self.path == "/datasets/build":
            self.write_json(202, response("accepted", job="dataset_build", request=body))
            return
        if self.path == "/adapters/train":
            self.write_json(501, response("not_configured", job="adapter_train", request=body))
            return
        if self.path == "/adapters/evaluate":
            self.write_json(202, response("accepted", job="adapter_evaluate", request=body))
            return
        self.write_json(404, {"error": "not found"})

    def read_json(self) -> dict[str, Any]:
        n = int(self.headers.get("content-length", "0") or "0")
        if n <= 0:
            return {}
        try:
            return json.loads(self.rfile.read(n))
        except json.JSONDecodeError:
            return {}

    def write_json(self, code: int, payload: dict[str, Any]) -> None:
        raw = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, fmt: str, *args: Any) -> None:
        print(fmt % args)


def main() -> None:
    port = int(os.environ.get("PORT", "8080"))
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
