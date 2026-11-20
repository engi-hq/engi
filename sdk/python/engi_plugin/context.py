"""The per-call Context handed to a node function."""

from __future__ import annotations

import base64
import copy
import logging
import threading
from typing import Any, Dict, List, Optional

from .errors import CancelledError, CredentialError, ParamError


class Call:
    """State of one execute request: cancellation and its in-flight callbacks."""

    def __init__(self, call_id: str = "") -> None:
        self.call_id = call_id
        self.cancelled = threading.Event()
        self.pending: set = set()


class Blob:
    """Reads and writes the bytes behind a BinaryRef through the host (stdio only)."""

    def __init__(self, ctx: "Context") -> None:
        self._ctx = ctx

    def read(self, ref: Dict[str, Any]) -> bytes:
        """Returns the bytes of the blob that ``ref`` (a BinaryRef dict) names."""
        res = self._ctx._request("blob.read", {"ref": ref})
        return base64.b64decode(res.get("data") or "")

    def write(self, name: str, mime: str, data: bytes) -> Dict[str, Any]:
        """Stores ``data`` as a blob and returns its BinaryRef dict."""
        if not isinstance(data, (bytes, bytearray, memoryview)):
            raise TypeError("blob.write data must be bytes, got %s" % type(data).__name__)
        payload = {"name": name, "mime": mime, "data": base64.b64encode(bytes(data)).decode("ascii")}
        return self._ctx._request("blob.write", payload)


class Context:
    """Everything a node needs for one execute call."""

    def __init__(self, params: Dict[str, Any], call: Call, transport: Any, logger: logging.Logger) -> None:
        self._inputs: List[List[Dict[str, Any]]] = params.get("inputs") or []
        self._params: List[Dict[str, Any]] = params.get("params") or [{}]
        self._param_errors: List[Dict[str, str]] = params.get("paramErrors") or []
        self._credentials: Dict[str, Any] = params.get("credentials") or {}
        self._credential_errors: Dict[str, str] = params.get("credentialErrors") or {}
        self._call = call
        self._transport = transport
        node = params.get("node") or {}
        self.node_type: str = node.get("type", "")
        self.node_version: int = node.get("version", 0)
        self.call_id: str = call.call_id
        self.execution: Dict[str, Any] = copy.deepcopy(params.get("execution") or {})
        #: Set when the host sends ``$/cancel``; long-running nodes should poll it.
        self.cancelled: threading.Event = call.cancelled
        self.log: logging.Logger = logger
        self.blob = Blob(self)

    def items(self, port: int = 0) -> List[Dict[str, Any]]:
        """Returns a deep copy of the items on main input ``port`` (empty if absent)."""
        if port < 0 or port >= len(self._inputs):
            return []
        return copy.deepcopy(self._inputs[port] or [])

    def param(self, name: str, item_index: int = 0, default: Any = None) -> Any:
        """Returns property ``name`` resolved for item ``item_index`` of input 0.

        Raises ParamError with the host's message if the property failed to resolve.
        An absent property returns ``default``.
        """
        if item_index < 0 or item_index >= len(self._params):
            raise IndexError("item_index %d out of range (%d parameter sets)" % (item_index, len(self._params)))
        if item_index < len(self._param_errors):
            msg = self._param_errors[item_index].get(name)
            if msg is not None:
                raise ParamError(msg, item_index=item_index)
        values = self._params[item_index]
        if name in values:
            return copy.deepcopy(values[name])
        return default

    def credential(self, name: str) -> Dict[str, Any]:
        """Returns the decrypted data of credential ``name``. Never log it."""
        if name in self._credential_errors:
            raise CredentialError(self._credential_errors[name])
        cred = self._credentials.get(name)
        if cred is None:
            raise CredentialError("credential %r is not bound to this node" % name)
        return copy.deepcopy(cred.get("data") or {})

    def eval(self, expression: str, item_index: int = 0) -> Any:
        """Evaluates an expression in the scope of item ``item_index`` (stdio only)."""
        res = self._request("expression.eval", {"expression": expression, "itemIndex": item_index})
        return res.get("value")

    def _request(self, method: str, params: Dict[str, Any]) -> Dict[str, Any]:
        if self._call.cancelled.is_set():
            raise CancelledError("call cancelled")
        full = dict(params)
        full["callId"] = self.call_id
        return self._transport.request(self._call, method, full)
