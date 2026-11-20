"""A fake host: drives a plugin over OS pipes."""

from __future__ import annotations

import json
import os
import threading
from typing import Any, Callable, Dict, List, Optional

from engi_plugin import Plugin


class FakeHost:
    def __init__(self, plugin: Plugin, on_request: Optional[Callable[[Dict[str, Any]], Dict[str, Any]]] = None) -> None:
        in_r, in_w = os.pipe()
        out_r, out_w = os.pipe()
        self._to_plugin = os.fdopen(in_w, "wb")
        self._from_plugin = os.fdopen(out_r, "rb")
        self._plugin_in = os.fdopen(in_r, "rb")
        self._plugin_out = os.fdopen(out_w, "wb")
        self.on_request = on_request
        self.notifications: List[Dict[str, Any]] = []
        self.responses: Dict[Any, Dict[str, Any]] = {}
        self._cv = threading.Condition()
        self._next = 0
        self.error: Optional[BaseException] = None
        self.raw_lines: List[bytes] = []
        self._plugin = plugin
        self._serve = threading.Thread(target=self._run_plugin, daemon=True)
        self._reader = threading.Thread(target=self._read, daemon=True)
        self._serve.start()
        self._reader.start()

    def _run_plugin(self) -> None:
        try:
            self._plugin.serve(self._plugin_in, self._plugin_out)
        except BaseException as e:  # reported by tests
            self.error = e
        finally:
            self._plugin_out.close()

    def _read(self) -> None:
        for line in self._from_plugin:
            self.raw_lines.append(line)
            msg = json.loads(line)
            if "method" in msg and "id" in msg and self.on_request:
                threading.Thread(target=self._answer, args=(msg,), daemon=True).start()
            elif "method" in msg:
                with self._cv:
                    self.notifications.append(msg)
                    self._cv.notify_all()
            else:
                with self._cv:
                    self.responses[msg["id"]] = msg
                    self._cv.notify_all()

    def _answer(self, msg: Dict[str, Any]) -> None:
        reply = {"jsonrpc": "2.0", "id": msg["id"]}
        reply.update(self.on_request(msg))
        try:
            self.send(reply)
        except (OSError, ValueError):
            pass

    def send(self, msg: Dict[str, Any]) -> None:
        self.send_raw(json.dumps(msg).encode() + b"\n")

    def send_raw(self, data: bytes) -> None:
        self._to_plugin.write(data)
        self._to_plugin.flush()

    def start(self, method: str, params: Any = None) -> int:
        self._next += 1
        self.send({"jsonrpc": "2.0", "id": self._next, "method": method, "params": params if params is not None else {}})
        return self._next

    def wait(self, req_id: Any, timeout: float = 5) -> Dict[str, Any]:
        with self._cv:
            if not self._cv.wait_for(lambda: req_id in self.responses, timeout):
                raise AssertionError("no response to %r" % req_id)
            return self.responses[req_id]

    def call(self, method: str, params: Any = None) -> Dict[str, Any]:
        return self.wait(self.start(method, params))

    def wait_notification(self, method: str, timeout: float = 5) -> Dict[str, Any]:
        with self._cv:
            if not self._cv.wait_for(lambda: any(n["method"] == method for n in self.notifications), timeout):
                raise AssertionError("no %s notification" % method)
            return next(n for n in self.notifications if n["method"] == method)

    def close(self) -> None:
        self._to_plugin.close()
        self._serve.join(5)
        self._reader.join(5)
        self._from_plugin.close()
        self._plugin_in.close()


def execute_params(node_type: str, items=None, params=None, **extra: Any) -> Dict[str, Any]:
    p: Dict[str, Any] = {
        "callId": "c1", "node": {"type": node_type, "version": 1},
        "inputs": [items if items is not None else []],
        "params": params if params is not None else [{}],
        "execution": {"executionId": "ex_1", "mode": "manual"},
    }
    p.update(extra)
    return p
