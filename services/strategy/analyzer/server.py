"""Private bounded static-analysis transport. No API/DB secrets or user execution."""
import json
import os
from pathlib import Path
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def load_config(path):
    config = json.loads(Path(path).read_text(encoding="utf-8"))
    if set(config) != {"max_source_bytes", "max_body_bytes", "memory_bytes", "cpu_seconds", "wall_seconds", "socket_timeout_seconds"}:
        raise ValueError("invalid analyzer configuration")
    if any(type(value) is not int for value in config.values()):
        raise ValueError("integer limits required")
    if not (1 <= config["max_source_bytes"] <= 100000 and 6 * config["max_source_bytes"] + 4096 <= config["max_body_bytes"] <= 2097152
            and 33554432 <= config["memory_bytes"] <= 268435456 and 1 <= config["cpu_seconds"] <= 3
            and 1 <= config["wall_seconds"] <= 3 and 1 <= config["socket_timeout_seconds"] <= 5):
        raise ValueError("invalid analyzer limits")
    return config


def analyze(source, config):
    payload = json.dumps({"source": source, "limits": config}).encode("utf-8")
    result = subprocess.run(
        [sys.executable, "-I", "-S", "-B", str(Path(__file__).with_name("check.py"))],
        input=payload, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        timeout=config["wall_seconds"], check=True, env={},
    )
    if len(result.stdout) > 16384:
        raise ValueError("invalid diagnostic response")
    return json.loads(result.stdout)


class Handler(BaseHTTPRequestHandler):
    def setup(self):
        super().setup()
        self.connection.settimeout(self.server.config["socket_timeout_seconds"])

    def log_message(self, *args):
        pass  # Never log source, request bodies or Python diagnostics.

    def reply(self, status, payload):
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        self.reply(200 if self.path in ("/healthz", "/readyz") else 404, {"status": "ok"})

    def do_POST(self):
        if self.path != "/lint":
            self.reply(404, {"error": "not_found"})
            return
        try:
            if self.headers.get("Content-Type", "").split(";")[0] != "application/json" or self.headers.get("Transfer-Encoding"):
                self.reply(415, {"error": "unsupported_media_type"})
                return
            lengths = self.headers.get_all("Content-Length", [])
            length = int(lengths[0]) if len(lengths) == 1 else -1
            if length < 1 or length > self.server.config["max_body_bytes"]:
                self.reply(413, {"error": "body_too_large"})
                return
            body = self.rfile.read(length)
            if len(body) != length:
                raise ValueError("incomplete body")
            payload = json.loads(body.decode("utf-8"))
            if not isinstance(payload, dict) or set(payload) != {"source", "language"} or payload["language"] != "python":
                raise ValueError("invalid input")
            source = payload["source"]
            if not isinstance(source, str) or len(source.encode("utf-8")) > self.server.config["max_source_bytes"] or "\0" in source:
                raise ValueError("invalid source")
        except (ValueError, UnicodeError, OSError):
            self.reply(400, {"error": "invalid_input"})
            return
        try:
            result = analyze(source, self.server.config)
        except (subprocess.SubprocessError, ValueError, OSError):
            self.reply(503, {"error": "analysis_unavailable"})
            return
        self.reply(200, result)


if __name__ == "__main__":
    config = load_config(os.environ.get("ANALYZER_CONFIG_FILE", str(Path(__file__).with_name("config.json"))))
    server = HTTPServer((os.environ.get("HTTP_HOST", "0.0.0.0"), int(os.environ.get("HTTP_PORT", "8080"))), Handler)
    server.config = config
    server.serve_forever()
