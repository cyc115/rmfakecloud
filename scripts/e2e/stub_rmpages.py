"""Tiny rm-pages stand-in for the e2e suite: POST /api/events accepts every
event by trace id, GET /healthz answers ok, POST /api/render/doc renders a
notebook as one blank page per page (so the viewer opens notebooks; PDFs get a
404 and rmfakecloud falls back to its own exporter). Usage: stub_rmpages.py [port]"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def blank_pdf(n):
    """A valid PDF of n blank 1404x1872 pages."""
    objs = ["<< /Type /Catalog /Pages 2 0 R >>",
            "<< /Type /Pages /Kids [%s] /Count %d >>" % (" ".join("%d 0 R" % (3 + i) for i in range(n)), n)]
    objs += ["<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1404 1872] >>"] * n
    out, offs = b"%PDF-1.4\n", []
    for i, o in enumerate(objs, 1):
        offs.append(len(out))
        out += b"%d 0 obj\n%s\nendobj\n" % (i, o.encode())
    xref = len(out)
    out += b"xref\n0 %d\n0000000000 65535 f \n" % (len(objs) + 1)
    out += b"".join(b"%010d 00000 n \n" % o for o in offs)
    out += b"trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objs) + 1, xref)
    return out


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
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.path == "/api/render/doc":
            try:
                req = json.loads(raw or b"{}")
            except ValueError:
                return self._send(400, {"error": "bad json"})
            if req.get("doc_type") != "notebook" or not req.get("pages"):
                return self._send(404, {"error": "not stubbed"})
            body = blank_pdf(len(req["pages"]))
            self.send_response(200)
            self.send_header("Content-Type", "application/pdf")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            return self.wfile.write(body)
        if self.path != "/api/events":
            return self._send(404, {"error": "not stubbed"})
        try:
            events = json.loads(raw or b"{}").get("events") or []
        except ValueError:
            return self._send(400, {"error": "bad json"})
        self._send(200, {"accepted": [e.get("traceId") or e.get("trace_id") for e in events]})

    def log_message(self, *_):
        pass


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", int(sys.argv[1]) if len(sys.argv) > 1 else 3098), Handler).serve_forever()
