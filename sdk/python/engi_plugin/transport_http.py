"""HTTP transport: JSON-RPC requests as ``POST /rpc``. Callbacks are unsupported."""

from __future__ import annotations

import hmac
import json
import logging
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Dict, Optional, Tuple

from .context import Call
from .errors import (
    CODE_BUSY, CODE_INTERNAL_ERROR, CODE_INVALID_REQUEST, CODE_PARSE_ERROR, RpcError, UnsupportedError,
)
from .plugin import Plugin


class HttpTransport:
    """Transport object handed to nodes: logs go to stderr, callbacks are refused."""

    def __init__(self, plugin: Plugin) -> None:
        self.logger = logging.Logger("engi_plugin." + plugin.name, logging.DEBUG)
        handler = logging.StreamHandler(sys.stderr)
        handler.setFormatter(logging.Formatter("%(levelname)s %(message)s"))
        self.logger.addHandler(handler)

    def call_logger(self, call_id: str) -> logging.Logger:
        """HTTP has no log callback: every call logs to stderr."""
        return self.logger

    def request(self, call: Call, method: str, params: Dict[str, Any]) -> Dict[str, Any]:
        raise UnsupportedError(method)


def _answer(plugin: Plugin, transport: HttpTransport, body: bytes) -> Tuple[int, Optional[Dict[str, Any]]]:
    """Returns (http status, JSON-RPC response or None for a notification)."""
    try:
        msg = json.loads(body)
    except ValueError:
        return 200, {"jsonrpc": "2.0", "id": None, "error": RpcError(CODE_PARSE_ERROR, "invalid JSON").to_wire()}
    if not isinstance(msg, dict) or not isinstance(msg.get("method"), str):
        return 200, {"jsonrpc": "2.0", "id": None,
                     "error": RpcError(CODE_INVALID_REQUEST, "not a JSON-RPC request").to_wire()}
    msg_id = msg.get("id")
    if msg_id is None:
        return 204, None  # $/cancel and shutdown have no meaning for a service
    resp: Dict[str, Any] = {"jsonrpc": "2.0", "id": msg_id}
    method = msg["method"]
    holds_slot = method == "execute"
    if holds_slot and not plugin.try_acquire_slot():
        resp["error"] = RpcError(CODE_BUSY, "too many concurrent requests").to_wire()
        return 200, resp
    try:
        resp["result"] = plugin.handle(method, msg.get("params"), transport)
        json.dumps(resp, allow_nan=False)
    except RpcError as e:
        resp.pop("result", None)
        resp["error"] = e.to_wire()
    except (ValueError, TypeError) as e:
        resp.pop("result", None)
        resp["error"] = RpcError(CODE_INTERNAL_ERROR, "encoding result: %s" % e).to_wire()
    finally:
        if holds_slot:
            plugin.release_slot()
    return 200, resp


def make_server(plugin: Plugin, host: str, port: int, token: Optional[str]) -> ThreadingHTTPServer:
    transport = HttpTransport(plugin)
    max_bytes = plugin.max_message_bytes

    class Handler(BaseHTTPRequestHandler):
        def _send(self, status: int, body: bytes = b"", ctype: str = "application/json") -> None:
            self.send_response(status)
            if body:
                self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self) -> None:  # noqa: N802 (http.server API)
            if self.path != "/rpc":
                self._send(404)
                return
            if token is not None:
                given = self.headers.get("Authorization", "")
                if not hmac.compare_digest(given.encode(), ("Bearer " + token).encode()):
                    self._send(401)
                    return
            try:
                length = int(self.headers.get("Content-Length", ""))
            except ValueError:
                self._send(411)
                return
            if length < 0 or length > max_bytes:
                self._send(413)
                return
            status, resp = _answer(plugin, transport, self.rfile.read(length))
            if resp is None:
                self._send(status)
                return
            data = json.dumps(resp, ensure_ascii=False, allow_nan=False).encode("utf-8")
            if len(data) > max_bytes:
                resp = {"jsonrpc": "2.0", "id": resp.get("id"),
                        "error": RpcError(CODE_INTERNAL_ERROR, "response exceeds the message limit").to_wire()}
                data = json.dumps(resp).encode("utf-8")
            self._send(status, data)

        def do_GET(self) -> None:  # noqa: N802
            self._send(405 if self.path == "/rpc" else 404)

        def log_message(self, format: str, *args: Any) -> None:
            return  # per-request access logs are noise; node logs still go to stderr

    server = ThreadingHTTPServer((host, port), Handler)
    server.daemon_threads = True
    return server
