"""Shared helpers for the unittest suites: a fake plugin server, the vector
loader and the subset matcher used by the conformance replay."""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[3]
VECTOR_DIR = REPO_ROOT / "conformance" / "vectors"
PLUGIN_PREFIX = "/__acd-ctx/plugin"
TOKEN = "0123456789abcdef0123456789abcdef0123"


class FakeServer:
    """A loopback HTTP server that answers with handler(method, path, headers, body).

    handler returns (status, json_value); a None value sends an empty body.
    """

    def __init__(self, handler):
        self.handler = handler
        self.requests = []
        server = self

        class _Handler(BaseHTTPRequestHandler):
            def _serve(self):
                length = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(length) if length else b""
                server.requests.append((self.command, self.path, dict(self.headers), body))
                status, value = server.handler(self.command, self.path, dict(self.headers), body)
                payload = b"" if value is None else json.dumps(value).encode("utf-8")
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            do_GET = _serve
            do_POST = _serve
            do_PUT = _serve

            def log_message(self, *args):  # keep test output quiet
                pass

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), _Handler)
        self.url = "http://127.0.0.1:%d" % self.httpd.server_address[1]
        self._thread = threading.Thread(target=self.httpd.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        self._thread.start()

    def close(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def load_suites(directory=VECTOR_DIR):
    """Return the parsed suite files in name order."""
    suites = []
    for path in sorted(Path(directory).glob("*.json")):
        with open(path, encoding="utf-8") as f:
            suites.append(json.load(f))
    return suites


def substitute(value, conv):
    """Replace {conv} in every string of a JSON value."""
    if isinstance(value, str):
        return value.replace("{conv}", conv)
    if isinstance(value, dict):
        return {k: substitute(v, conv) for k, v in value.items()}
    if isinstance(value, list):
        return [substitute(v, conv) for v in value]
    return value


def match(expected, actual, at="$"):
    """Return None when actual contains expected, else a reason string.

    Objects match by key (subset), arrays by length and position, "$any"
    matches any present value, other scalars must be equal.
    """
    if isinstance(expected, str) and expected == "$any":
        return None if actual is not None else "%s: value is null or missing" % at
    if isinstance(expected, dict):
        if not isinstance(actual, dict):
            return "%s: want object, got %r" % (at, type(actual).__name__)
        for k, ev in expected.items():
            if k not in actual:
                return "%s: missing %r" % (at, k)
            reason = match(ev, actual[k], "%s.%s" % (at, k))
            if reason:
                return reason
        return None
    if isinstance(expected, list):
        if not isinstance(actual, list):
            return "%s: want array, got %r" % (at, type(actual).__name__)
        if len(expected) != len(actual):
            return "%s: want %d items, got %d" % (at, len(expected), len(actual))
        for i, ev in enumerate(expected):
            reason = match(ev, actual[i], "%s[%d]" % (at, i))
            if reason:
                return reason
        return None
    if isinstance(expected, bool) or isinstance(actual, bool):
        ok = expected is actual
    else:
        ok = expected == actual
    return None if ok else "%s: want %r, got %r" % (at, expected, actual)


def wildcard_to_placeholder(value):
    """The fake server answers "$any" fields with a placeholder value."""
    if isinstance(value, str) and value == "$any":
        return "x"
    if isinstance(value, dict):
        return {k: wildcard_to_placeholder(v) for k, v in value.items()}
    if isinstance(value, list):
        return [wildcard_to_placeholder(v) for v in value]
    return value


def expected_auth_ok(auth, header):
    """Check the Authorization header against a vector's auth field."""
    if auth is True:
        return header == "Bearer " + TOKEN
    if auth is False:
        return header is None
    if auth == "wrong":
        return header is not None and header.startswith("Bearer ") and header != "Bearer " + TOKEN
    return False


class ReplayServer(FakeServer):
    """Fake server that checks each request against the next expected step of a
    vector and answers with that step's expected response. Mismatches are kept
    in self.mismatches and reported by the test."""

    def __init__(self, steps, conv):
        self.steps = steps
        self.conv = conv
        self.index = 0
        self.mismatches = []
        super().__init__(self._answer)

    def _answer(self, method, path, headers, body):
        if self.index >= len(self.steps):
            self.mismatches.append("unexpected request %s %s" % (method, path))
            return 500, {"error": "unexpected request"}
        step = self.steps[self.index]
        self.index += 1
        req = step["request"]
        want_path = PLUGIN_PREFIX + "/" + substitute(req["path"], self.conv)
        if method != req["method"] or path != want_path:
            self.mismatches.append("step %d: got %s %s, want %s %s"
                                   % (self.index - 1, method, path, req["method"], want_path))
        if not expected_auth_ok(req.get("auth", True), headers.get("Authorization")):
            self.mismatches.append("step %d: auth header %r does not match %r"
                                   % (self.index - 1, headers.get("Authorization"), req.get("auth", True)))
        if "raw" in req:
            if body != req["raw"].encode("utf-8"):
                self.mismatches.append("step %d: raw body %r" % (self.index - 1, body))
        elif "body" in req:
            try:
                got = json.loads(body)
            except ValueError:
                got = object()
            if got != substitute(req["body"], self.conv):
                self.mismatches.append("step %d: body %r does not match" % (self.index - 1, body))
        elif body:
            self.mismatches.append("step %d: unexpected body %r" % (self.index - 1, body))
        exp = step["expect"]
        value = wildcard_to_placeholder(exp.get("json"))
        if exp.get("keys"):
            value = dict(value or {})
            for key in exp["keys"]:
                value.setdefault(key, "x")
        return exp["status"], value
