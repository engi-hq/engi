"""Errors and JSON-RPC codes of the Engi Plugin Protocol v1 (docs/15 section 15.3)."""

from __future__ import annotations

from typing import Any, Dict, Optional

CODE_PARSE_ERROR = -32700
CODE_INVALID_REQUEST = -32600
CODE_METHOD_NOT_FOUND = -32601
CODE_INVALID_PARAMS = -32602
CODE_INTERNAL_ERROR = -32603
#: A failure shown to the user like a built-in node's error.
CODE_NODE_ERROR = -32001
#: Answers a request that was cancelled.
CODE_CANCELLED = -32002
#: Answers a method the transport does not offer (for example a callback over HTTP).
CODE_UNSUPPORTED = -32003
#: Answers a request above the plugin's concurrency cap.
CODE_BUSY = -32004


class RpcError(Exception):
    """A JSON-RPC error object, raised or received."""

    def __init__(self, code: int, message: str, data: Any = None) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.data = data

    def to_wire(self) -> Dict[str, Any]:
        err: Dict[str, Any] = {"code": self.code, "message": self.message}
        if self.data is not None:
            err["data"] = self.data
        return err


class NodeError(Exception):
    """Fails the node with a message shown to the user (JSON-RPC code -32001)."""

    def __init__(
        self,
        message: str,
        description: Optional[str] = None,
        http_status: Optional[int] = None,
        item_index: Optional[int] = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.description = description
        self.http_status = http_status
        self.item_index = item_index

    def to_rpc(self) -> RpcError:
        data: Dict[str, Any] = {}
        if self.description:
            data["description"] = self.description
        if self.http_status:
            data["httpStatus"] = self.http_status
        if self.item_index is not None:
            data["itemIndex"] = self.item_index
        return RpcError(CODE_NODE_ERROR, self.message, data or None)


class ParamError(NodeError):
    """A parameter the host could not resolve; the message is the host's."""


class CredentialError(NodeError):
    """A credential that is not declared, not bound, or could not be loaded."""


class HostError(NodeError):
    """The host answered a callback with an error. Uncaught, it fails the node."""

    def __init__(self, message: str, code: int = CODE_INTERNAL_ERROR, **kwargs: Any) -> None:
        super().__init__(message, **kwargs)
        self.code = code


class UnsupportedError(HostError):
    """The callback is not available over this transport (code -32003)."""

    def __init__(self, what: str) -> None:
        super().__init__(what + " is not supported over this transport", CODE_UNSUPPORTED)

    def to_rpc(self) -> RpcError:
        return RpcError(CODE_UNSUPPORTED, self.message)


class CancelledError(Exception):
    """The host cancelled the call (`$/cancel`); answered with code -32002."""


class DescriptorError(Exception):
    """A node descriptor breaks a v1 rule (docs/15 section 15.4)."""


class MessageTooLargeError(Exception):
    """A message exceeded the configured size limit; the connection is closed."""
