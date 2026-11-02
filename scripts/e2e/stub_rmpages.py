"""Tiny rm-pages stand-in for the e2e suite: POST /api/events accepts every
event by trace id, GET /healthz answers ok. Usage: stub_rmpages.py [port]"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self._send(200, {"status": "ok"})

    def do_POST(self):
        if self.path != "/api/events":
            # /api/render/doc is not stubbed: rmfakecloud falls back to its own exporter
            return self._send(404, {"error": "not stubbed"})
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        try:
            events = json.loads(raw or b"{}").get("events") or []
        except ValueError:
            return self._send(400, {"error": "bad json"})
        self._send(200, {"accepted": [e.get("traceId") or e.get("trace_id") for e in events]})

    def log_message(self, *_):
        pass


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", int(sys.argv[1]) if len(sys.argv) > 1 else 3098), Handler).serve_forever()
