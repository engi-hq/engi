"""stdio transport: NDJSON JSON-RPC 2.0 over a pair of binary streams."""

from __future__ import annotations

import json
import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, Optional

from .context import Call
from .errors import (
    CODE_BUSY, CODE_CANCELLED, CODE_INTERNAL_ERROR, CODE_INVALID_REQUEST, CODE_METHOD_NOT_FOUND,
    CODE_NODE_ERROR, CODE_PARSE_ERROR, CODE_UNSUPPORTED, CancelledError, HostError,
    MessageTooLargeError, RpcError, UnsupportedError,
)
from .plugin import Plugin, make_logger


class _Pending:
    def __init__(self) -> None:
        self.event = threading.Event()
        self.result: Any = None
        self.error: Optional[Dict[str, Any]] = None
        self.cancelled = False


def _id_key(msg_id: Any) -> str:
    return json.dumps(msg_id, sort_keys=True)


class StdioSession:
    """One stdio connection to the host."""

    def __init__(self, plugin: Plugin, reader: Any, writer: Any) -> None:
        self._plugin = plugin
        self._reader = reader
        self._writer = writer
        self._max = plugin.max_message_bytes
        self._wlock = threading.Lock()
        self._lock = threading.Lock()
        self._next_id = 0
        self._pending: Dict[int, _Pending] = {}
        self._calls: Dict[str, Call] = {}
        self._inflight = 0
        self._shutdown = False
        self._done = threading.Event()
        self._fatal: Optional[Exception] = None
        self._pool = ThreadPoolExecutor(max_workers=plugin.max_concurrent_calls,
                                        thread_name_prefix="engi-plugin")
        self.logger = make_logger("engi_plugin." + plugin.name, self._send_log)

    def call_logger(self, call_id: str) -> logging.Logger:
        """A logger whose lines carry ``call_id``, so the host logs them with that run."""
        return make_logger("engi_plugin." + self._plugin.name,
                           lambda level, message, attrs: self._send_log(level, message, attrs, call_id))

    # -- lifecycle --------------------------------------------------------

    def run(self) -> None:
        threading.Thread(target=self._read_loop, name="engi-plugin-reader", daemon=True).start()
        self._done.wait()
        self._cancel_all()
        self._pool.shutdown(wait=True)
        if self._fatal is not None:
            raise self._fatal

    def _read_loop(self) -> None:
        try:
            while True:
                try:
                    line = self._read_line()
                except MessageTooLargeError as e:
                    self._fatal = e
                    return
                except (OSError, ValueError):
                    return
                if line is None:
                    return
                if not line.strip():
                    continue
                try:
                    msg = json.loads(line)
                except ValueError:
                    self._reply_error(None, RpcError(CODE_PARSE_ERROR, "invalid JSON"))
                    continue
                if not isinstance(msg, dict):
                    self._reply_error(None, RpcError(CODE_INVALID_REQUEST, "message must be an object"))
                    continue
                self._dispatch(msg)
        finally:
            self._done.set()

    def _read_line(self) -> Optional[bytes]:
        line = self._reader.readline(self._max + 1)
        if not line:
            return None
        if len(line) > self._max:
            raise MessageTooLargeError("message exceeds the limit of %d bytes" % self._max)
        return line

    # -- inbound ----------------------------------------------------------

    def _dispatch(self, msg: Dict[str, Any]) -> None:
        method = msg.get("method")
        msg_id = msg.get("id")
        if method is None:
            if msg_id is not None:
                self._resolve(msg)
            return
        params = msg.get("params")
        if method == "$/cancel":
            if isinstance(params, dict):
                self._cancel(params.get("id"))
            return
        if method == "shutdown":
            with self._lock:
                self._shutdown = True
                idle = self._inflight == 0
            if msg_id is not None:
                self._reply(msg_id, {})
            if idle:
                self._done.set()
            return
        if msg_id is None:
            return  # unknown notification: ignored
        if method == "execute":
            self._start_execute(msg_id, params)
            return
        try:
            self._reply(msg_id, self._plugin.handle(method, params, self))
        except RpcError as e:
            self._reply_error(msg_id, e)

    def _start_execute(self, msg_id: Any, params: Any) -> None:
        with self._lock:
            closing = self._shutdown
        if closing:
            self._reply_error(msg_id, RpcError(CODE_INVALID_REQUEST, "plugin is shutting down"))
            return
        if not self._plugin.try_acquire_slot():
            self._reply_error(msg_id, RpcError(CODE_BUSY, "too many concurrent requests"))
            return
        call = Call()
        key = _id_key(msg_id)
        with self._lock:
            self._calls[key] = call
            self._inflight += 1
        self._pool.submit(self._run_execute, msg_id, key, params, call)

    def _run_execute(self, msg_id: Any, key: str, params: Any, call: Call) -> None:
        try:
            try:
                self._reply(msg_id, self._plugin.handle("execute", params, self, call))
            except RpcError as e:
                self._reply_error(msg_id, e)
            except Exception as e:  # defensive: never leave a request unanswered
                self._reply_error(msg_id, RpcError(CODE_INTERNAL_ERROR, "%s: %s" % (type(e).__name__, e)))
        finally:
            self._plugin.release_slot()
            with self._lock:
                self._calls.pop(key, None)
                self._inflight -= 1
                finished = self._shutdown and self._inflight == 0
            if finished:
                self._done.set()

    def _resolve(self, msg: Dict[str, Any]) -> None:
        with self._lock:
            pend = self._pending.get(msg.get("id")) if isinstance(msg.get("id"), int) else None
        if pend is None:
            return
        pend.result = msg.get("result")
        pend.error = msg.get("error")
        pend.event.set()

    def _cancel(self, msg_id: Any) -> None:
        with self._lock:
            call = self._calls.get(_id_key(msg_id))
            if call is not None:
                self._cancel_call_locked(call)

    def _cancel_call_locked(self, call: Call) -> None:
        call.cancelled.set()
        for pid in list(call.pending):
            pend = self._pending.get(pid)
            if pend is not None:
                pend.cancelled = True
                pend.event.set()

    def _cancel_all(self) -> None:
        with self._lock:
            for call in self._calls.values():
                self._cancel_call_locked(call)

    # -- outbound ---------------------------------------------------------

    def _write(self, msg: Dict[str, Any]) -> bool:
        msg["jsonrpc"] = "2.0"
        data = json.dumps(msg, ensure_ascii=False, allow_nan=False).encode("utf-8") + b"\n"
        if len(data) > self._max:
            raise MessageTooLargeError("sending %d bytes exceeds the limit of %d" % (len(data), self._max))
        with self._wlock:
            try:
                self._writer.write(data)
                self._writer.flush()
            except (OSError, ValueError):
                return False
        return True

    def _reply(self, msg_id: Any, result: Any) -> None:
        try:
            self._write({"id": msg_id, "result": result})
        except (MessageTooLargeError, ValueError, TypeError) as e:
            self._reply_error(msg_id, RpcError(CODE_INTERNAL_ERROR, "encoding result: %s" % e))

    def _reply_error(self, msg_id: Any, err: RpcError) -> None:
        try:
            self._write({"id": msg_id, "error": err.to_wire()})
        except (MessageTooLargeError, ValueError, TypeError):
            self._write({"id": msg_id, "error": RpcError(CODE_INTERNAL_ERROR, err.message[:200]).to_wire()})

    def _send_log(self, level: str, message: str, attrs: Optional[Dict[str, Any]], call_id: str = "") -> None:
        params: Dict[str, Any] = {"level": level, "message": message}
        if attrs:
            params["attrs"] = attrs
        if call_id:
            params["callId"] = call_id
        try:
            self._write({"method": "log", "params": params})
        except (MessageTooLargeError, ValueError, TypeError):
            pass

    # -- callbacks (plugin -> host) ----------------------------------------

    def request(self, call: Call, method: str, params: Dict[str, Any]) -> Dict[str, Any]:
        pend = _Pending()
        with self._lock:
            if call.cancelled.is_set():
                raise CancelledError("call cancelled")
            self._next_id += 1
            pid = self._next_id
            self._pending[pid] = pend
            call.pending.add(pid)
        try:
            try:
                sent = self._write({"id": pid, "method": method, "params": params})
            except (MessageTooLargeError, ValueError, TypeError) as e:
                raise HostError("sending %s: %s" % (method, e))
            if not sent:
                raise HostError("host connection closed")
            pend.event.wait()
        finally:
            with self._lock:
                self._pending.pop(pid, None)
                call.pending.discard(pid)
        if pend.cancelled:
            raise CancelledError("call cancelled")
        if pend.error is not None:
            raise _host_error(pend.error)
        return pend.result if isinstance(pend.result, dict) else {}


def _host_error(err: Dict[str, Any]) -> HostError:
    code = err.get("code", CODE_INTERNAL_ERROR)
    message = err.get("message", "host error")
    if code == CODE_UNSUPPORTED:
        e = UnsupportedError("callback")
        e.message = message
        e.args = (message,)
        return e
    data = err.get("data") if code == CODE_NODE_ERROR and isinstance(err.get("data"), dict) else {}
    return HostError(message, code, description=data.get("description"),
                     http_status=data.get("httpStatus"), item_index=data.get("itemIndex"))
