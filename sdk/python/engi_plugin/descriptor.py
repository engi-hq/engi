"""Node descriptors and their wire form (mirrors pkg/nodesdk and pkg/pluginproto)."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Sequence

PROPERTY_TYPES = (
    "string", "number", "boolean", "options", "multiOptions", "json", "code", "dateTime",
    "collection", "fixedCollection", "resourceLocator", "credentialsSelect", "notice",
)
#: Groups a v1 plugin may declare.
ALLOWED_GROUPS = ("action", "transform", "flow")
MERGE_POLICIES = ("", "waitAll", "any", "partialOnFlush")
PORT_MAIN = "main"
RESERVED_TYPE_PREFIX = "engi."

_TYPE_RE = re.compile(r"^[a-zA-Z][a-zA-Z0-9_-]*(\.[a-zA-Z][a-zA-Z0-9_-]*)+$")


def _compact(d: Dict[str, Any], always: Sequence[str] = ()) -> Dict[str, Any]:
    """Drops empty values like Go's omitempty, except the keys in always."""
    return {
        k: v for k, v in d.items()
        if k in always or not (v is None or v is False or v == 0 or v == "" or v == [] or v == {})
    }


@dataclass
class Option:
    """One choice of an options property."""

    name: str
    value: Any
    description: str = ""

    def to_wire(self) -> Dict[str, Any]:
        return _compact({"name": self.name, "value": self.value, "description": self.description},
                        always=("name", "value"))


@dataclass
class DisplayOptions:
    """Shows or hides a property depending on other parameter values."""

    show: Dict[str, List[Any]] = field(default_factory=dict)
    hide: Dict[str, List[Any]] = field(default_factory=dict)

    def to_wire(self) -> Dict[str, Any]:
        return _compact({"show": self.show, "hide": self.hide})


@dataclass
class Property:
    """One entry of a node's typed parameter schema.

    ``default=None`` means "no default" and is omitted from the wire form.
    """

    name: str
    type: str = "string"
    display_name: str = ""
    default: Any = None
    required: bool = False
    placeholder: str = ""
    description: str = ""
    options: List[Option] = field(default_factory=list)
    children: List["Property"] = field(default_factory=list)
    display_options: Optional[DisplayOptions] = None

    def to_wire(self) -> Dict[str, Any]:
        d = _compact({
            "name": self.name,
            "displayName": self.display_name,
            "type": self.type,
            "required": self.required,
            "placeholder": self.placeholder,
            "description": self.description,
            "options": [o.to_wire() for o in self.options],
            "children": [c.to_wire() for c in self.children],
        }, always=("name", "type"))
        if self.default is not None:
            d["default"] = self.default
        if self.display_options is not None:
            do = self.display_options.to_wire()
            if do:
                d["displayOptions"] = do
        return d


@dataclass
class Port:
    """An input or output port. v1 plugins may only use ``main`` ports."""

    name: str = PORT_MAIN
    kind: str = ""
    display_name: str = ""
    required: bool = False
    max_connections: int = 0

    def effective_kind(self) -> str:
        return self.kind or PORT_MAIN

    def to_wire(self) -> Dict[str, Any]:
        return _compact({
            "name": self.name, "kind": self.kind, "displayName": self.display_name,
            "required": self.required, "maxConnections": self.max_connections,
        }, always=("name",))


@dataclass
class CredentialRef:
    """A credential the node can use; ``name`` is the key for ``ctx.credential``."""

    name: str
    type: str
    required: bool = False

    def to_wire(self) -> Dict[str, Any]:
        return _compact({"name": self.name, "type": self.type, "required": self.required},
                        always=("name", "type"))


@dataclass
class Descriptor:
    """A node type's metadata, sent in the ``initialize`` result."""

    type: str
    version: int = 1
    display_name: str = ""
    description: str = ""
    group: str = "action"
    icon: str = ""
    inputs: List[Port] = field(default_factory=lambda: [Port()])
    outputs: List[Port] = field(default_factory=lambda: [Port()])
    properties: List[Property] = field(default_factory=list)
    credentials: List[CredentialRef] = field(default_factory=list)
    merge_policy: str = ""

    def to_wire(self) -> Dict[str, Any]:
        return _compact({
            "type": self.type,
            "version": self.version,
            "displayName": self.display_name or self.type,
            "description": self.description,
            "group": self.group,
            "icon": self.icon,
            "inputs": [p.to_wire() for p in self.inputs],
            "outputs": [p.to_wire() for p in self.outputs],
            "properties": [p.to_wire() for p in self.properties],
            "credentials": [c.to_wire() for c in self.credentials],
            "mergePolicy": self.merge_policy,
        }, always=("type", "version", "displayName", "group"))


def check_descriptor(d: Descriptor) -> List[str]:
    """Returns why a plugin may not declare d (docs/15 section 15.4); empty when valid.

    Mirrors pluginproto.CheckDescriptor except for the manifest-listing rule, which
    needs the manifest and is checked by the host.
    """
    errs: List[str] = []
    if not _TYPE_RE.match(d.type or ""):
        errs.append("node type %r: use dot-separated names such as acme.text.reverse" % d.type)
    if (d.type or "").startswith(RESERVED_TYPE_PREFIX):
        errs.append("node type %r: the %r prefix is reserved for built-in nodes"
                    % (d.type, RESERVED_TYPE_PREFIX))
    if d.group not in ALLOWED_GROUPS:
        errs.append("group %r: plugins may declare %s nodes only" % (d.group, "/".join(ALLOWED_GROUPS)))
    if not isinstance(d.version, int) or isinstance(d.version, bool) or d.version < 1:
        errs.append("version must be an integer of at least 1")
    if d.merge_policy not in MERGE_POLICIES:
        errs.append("unknown merge policy %r" % d.merge_policy)
    for direction, ports in (("inputs", d.inputs), ("outputs", d.outputs)):
        for i, p in enumerate(ports):
            if p.effective_kind() != PORT_MAIN:
                errs.append("%s[%d] %r: plugins may only use main ports, not %r"
                            % (direction, i, p.name, p.kind))
            if p.max_connections:
                errs.append("%s[%d]: maxConnections only applies to sub-port inputs" % (direction, i))
    seen = set()
    for i, p in enumerate(d.properties):
        if not p.name:
            errs.append("properties[%d]: name must not be empty" % i)
            continue
        if p.name in seen:
            errs.append("properties[%d]: duplicate name %r" % (i, p.name))
        seen.add(p.name)
        if p.type not in PROPERTY_TYPES:
            errs.append("properties[%d] %r: unknown type %r" % (i, p.name, p.type))
    return errs
