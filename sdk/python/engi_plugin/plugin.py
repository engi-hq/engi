"""Plugin: node registry, request dispatch and the two transports."""

from __future__ import annotations

import datetime
import logging
import sys
import threading
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Sequence, Tuple, Union

from .context import Call, Context
from .descriptor import CredentialRef, Descriptor, Port, Property, check_descriptor
from .errors import (
    CODE_INTERNAL_ERROR, CODE_INVALID_PARAMS, CODE_INVALID_REQUEST, CODE_METHOD_NOT_FOUND,
    CancelledError, CODE_CANCELLED, DescriptorError, NodeError, RpcError,
)

PROTOCOL_VERSION = 1
#: Default cap on one protocol message (bytes). Match the host's plugins.maxMessageBytes.
DEFAULT_MAX_MESSAGE_BYTES = 32 << 20
#: Default number of execute requests handled at once. Match plugins.maxConcurrentCalls.
DEFAULT_MAX_CONCURRENT_CALLS = 16

_LEVELS = ((logging.ERROR, "error"), (logging.WARNING, "warn"), (logging.INFO, "info"))


def log_level_name(levelno: int) -> str:
    """Maps a logging level to a protocol log level."""
    for no, name in _LEVELS:
        if levelno >= no:
            return name
    return "debug"


@dataclass
class Wait:
    """Pauses the execution until a time or a resume token."""

    until: Union[datetime.datetime, str, None] = None
    resume_token: str = ""

    def to_wire(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {}
        if isinstance(self.until, datetime.datetime):
            u = self.until
            if u.tzinfo is None:
                u = u.replace(tzinfo=datetime.timezone.utc)
            d["until"] = u.isoformat()
        elif self.until:
            d["until"] = self.until
        if self.resume_token:
            d["resumeToken"] = self.resume_token
        return d


@dataclass
class Output:
    """A node result with a wait request; a plain list of lists needs no wrapper."""

    outputs: List[List[Dict[str, Any]]] = field(default_factory=list)
    wait: Optional[Wait] = None


NodeFunc = Callable[[Context], Any]


class _HostLogHandler(logging.Handler):
    def __init__(self, sink: Callable[[str, str, Optional[Dict[str, Any]]], None]) -> None:
        super().__init__()
        self._sink = sink
        self.setFormatter(logging.Formatter("%(message)s"))

    def emit(self, record: logging.LogRecord) -> None:
        try:
            self._sink(log_level_name(record.levelno), self.format(record), getattr(record, "attrs", None))
        except Exception:  # never let logging break a node
            self.handleError(record)


def make_logger(name: str, sink: Callable[[str, str, Optional[Dict[str, Any]]], None]) -> logging.Logger:
    """A standalone logger whose records go to ``sink(level, message, attrs)``."""
    logger = logging.Logger(name, logging.DEBUG)
    logger.addHandler(_HostLogHandler(sink))
    return logger


class Plugin:
    """An Engi plugin: register nodes with ``@plugin.node`` and call ``serve()``."""

    def __init__(
        self,
        name: str,
        version: str,
        max_message_bytes: int = DEFAULT_MAX_MESSAGE_BYTES,
        max_concurrent_calls: int = DEFAULT_MAX_CONCURRENT_CALLS,
    ) -> None:
        if max_message_bytes < 1 or max_concurrent_calls < 1:
            raise ValueError("max_message_bytes and max_concurrent_calls must be positive")
        self.name = name
        self.version = version
        self.max_message_bytes = max_message_bytes
        self.max_concurrent_calls = max_concurrent_calls
        self._nodes: Dict[Tuple[str, int], Tuple[Descriptor, NodeFunc]] = {}
        self._slots = threading.BoundedSemaphore(max_concurrent_calls)

    # -- registration -----------------------------------------------------

    def node(
        self,
        type: str,
        version: int = 1,
        display_name: str = "",
        group: str = "action",
        description: str = "",
        icon: str = "",
        properties: Sequence[Property] = (),
        inputs: Optional[Sequence[Union[Port, str]]] = None,
        outputs: Optional[Sequence[Union[Port, str]]] = None,
        credentials: Sequence[CredentialRef] = (),
        merge_policy: str = "",
    ) -> Callable[[NodeFunc], NodeFunc]:
        """Decorator registering ``run(ctx)`` as node ``type``.

        ``inputs``/``outputs`` default to one ``main`` port each; pass ``[]`` for none.
        Port entries may be ``Port`` objects or names.
        """
        def ports(v: Optional[Sequence[Union[Port, str]]]) -> List[Port]:
            if v is None:
                return [Port()]
            return [p if isinstance(p, Port) else Port(name=p) for p in v]

        desc = Descriptor(
            type=type, version=version, display_name=display_name, group=group,
            description=description, icon=icon, inputs=ports(inputs), outputs=ports(outputs),
            properties=list(properties), credentials=list(credentials), merge_policy=merge_policy,
        )

        def register(fn: NodeFunc) -> NodeFunc:
            key = (type, version)
            if key in self._nodes:
                raise DescriptorError("node %s version %d is registered twice" % key)
            self._nodes[key] = (desc, fn)
            return fn

        return register

    def descriptors(self) -> List[Descriptor]:
        return [d for d, _ in self._nodes.values()]

    def validate(self) -> None:
        """Raises DescriptorError if any descriptor breaks a v1 rule."""
        errs: List[str] = []
        if not self._nodes:
            errs.append("plugin %r declares no nodes" % self.name)
        for d in self.descriptors():
            errs.extend("node %r version %d: %s" % (d.type, d.version, e) for e in check_descriptor(d))
        if errs:
            raise DescriptorError("; ".join(errs))

    # -- dispatch ---------------------------------------------------------

    def handle(self, method: str, params: Any, transport: Any, call: Optional[Call] = None) -> Dict[str, Any]:
        """Serves one request; raises RpcError. Shared by both transports."""
        if method == "initialize":
            host_version = params.get("protocolVersion") if isinstance(params, dict) else None
            if not isinstance(host_version, int) or host_version < PROTOCOL_VERSION:
                raise RpcError(CODE_INVALID_PARAMS, "unsupported protocol version %r; this plugin speaks %d"
                               % (host_version, PROTOCOL_VERSION))
            return {
                "protocolVersion": PROTOCOL_VERSION,
                "plugin": {"name": self.name, "version": self.version},
                "nodes": [d.to_wire() for d in self.descriptors()],
            }
        if method == "health":
            return {}
        if method == "execute":
            return self._execute(params, transport, call or Call())
        raise RpcError(CODE_METHOD_NOT_FOUND, "method %r not found" % method)

    def _execute(self, params: Any, transport: Any, call: Call) -> Dict[str, Any]:
        if not isinstance(params, dict) or not isinstance(params.get("node"), dict):
            raise RpcError(CODE_INVALID_PARAMS, "execute params must be an object with a node")
        ref = params["node"]
        entry = self._nodes.get((ref.get("type"), ref.get("version")))
        if entry is None:
            raise RpcError(CODE_INVALID_PARAMS, "unknown node %s version %s" % (ref.get("type"), ref.get("version")))
        desc, fn = entry
        call.call_id = params.get("callId") or ""
        ctx = Context(params, call, transport, transport.call_logger(call.call_id))
        try:
            result = fn(ctx)
        except RpcError:
            raise
        except NodeError as e:
            raise e.to_rpc()
        except CancelledError:
            raise RpcError(CODE_CANCELLED, "cancelled")
        except Exception as e:
            ctx.log.error("node %s failed", desc.type, exc_info=True)
            raise RpcError(CODE_INTERNAL_ERROR, "%s: %s" % (type(e).__name__, e))
        return self._normalize(desc, result)

    @staticmethod
    def _normalize(desc: Descriptor, result: Any) -> Dict[str, Any]:
        wait = None
        if isinstance(result, Output):
            result, wait = result.outputs, result.wait
        bad = None
        if not isinstance(result, (list, tuple)) or not all(isinstance(o, (list, tuple)) for o in result):
            bad = "a node must return a list with one list of items per output port"
        elif len(result) != len(desc.outputs):
            bad = "node %s declares %d output port(s) but returned %d" % (desc.type, len(desc.outputs), len(result))
        elif not all(isinstance(i, dict) and isinstance(i.get("json"), dict) for o in result for i in o):
            bad = 'every item must be a dict with a "json" dict'
        if bad:
            raise RpcError(CODE_INTERNAL_ERROR, "invalid node output: " + bad)
        out: Dict[str, Any] = {"outputs": [list(o) for o in result]}
        if wait is not None:
            w = wait.to_wire()
            if w:
                out["wait"] = w
        return out

    def try_acquire_slot(self) -> bool:
        return self._slots.acquire(blocking=False)

    def release_slot(self) -> None:
        self._slots.release()

    # -- transports -------------------------------------------------------

    def serve(self, stdin: Any = None, stdout: Any = None) -> None:
        """Serves the stdio transport until shutdown or EOF.

        Streams are binary file objects (default: the process's stdin/stdout). While
        serving, ``sys.stdout`` is redirected to stderr so stray prints cannot corrupt
        the protocol stream. Raises DescriptorError before serving if a descriptor is
        invalid and MessageTooLargeError if the host sends an oversized message.
        """
        from .transport_stdio import StdioSession

        self.validate()
        reader = stdin if stdin is not None else sys.stdin.buffer
        writer = stdout if stdout is not None else sys.stdout.buffer
        saved = sys.stdout
        sys.stdout = sys.stderr
        try:
            StdioSession(self, reader, writer).run()
        finally:
            sys.stdout = saved

    def http_server(self, host: str, port: int, token: Optional[str] = None) -> Any:
        """Returns a ThreadingHTTPServer for ``POST /rpc`` (not yet serving).

        ``port=0`` picks a free port (see ``server.server_address``). If ``token`` is
        set, requests need ``Authorization: Bearer <token>``.
        """
        from .transport_http import make_server

        self.validate()
        return make_server(self, host, port, token)

    def serve_http(self, host: str, port: int, token: Optional[str] = None) -> None:
        """Serves the HTTP transport until interrupted. Callbacks are unsupported."""
        server = self.http_server(host, port, token)
        try:
            server.serve_forever()
        except KeyboardInterrupt:
            pass
        finally:
            server.server_close()
