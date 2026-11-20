"""Private parser transport; a bounded disposable child decodes each upload."""
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import os
from pathlib import Path
import subprocess
import sys

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, status, payload):
        b = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def setup(self):
        super().setup()
        self.connection.settimeout(8)

    def do_GET(self):
        self.reply(200 if self.path in ("/readyz", "/healthz") else 404, {"status": "ok"})

    def do_POST(self):
        if self.path != "/parse":
            self.reply(404, {"error": "not_found"})
            return
        try:
            lengths = self.headers.get_all("Content-Length", [])
            length = int(lengths[0]) if len(lengths) == 1 else -1
            if not 0 < length <= 145 * 1024**2 or self.headers.get("Transfer-Encoding") or self.headers.get("Content-Type") != "application/json":
                raise ValueError()
            data = self.rfile.read(length)
            if len(data) != length:
                raise ValueError()
            result = subprocess.run([sys.executable, "-I", "-B", str(Path(__file__).with_name("parse.py"))],
                                    input=data, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                    timeout=6, check=True,
                                    env={"PYTHONDONTWRITEBYTECODE":"1", "OMP_NUM_THREADS":"1", "OPENBLAS_NUM_THREADS":"1"})
            if len(result.stdout) > 32 * 1024**2:
                raise ValueError()
            self.reply(200, json.loads(result.stdout))
        except subprocess.TimeoutExpired:
            self.reply(503, {"error": "decoder_limit"})
        except (ValueError, OSError, subprocess.SubprocessError):
            self.reply(400, {"error": "invalid_dataset"})

if __name__ == "__main__":
    HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
