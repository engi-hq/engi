# Engi Python Code node runner.
#
# Started once per run as `python -I -c <this script>` with an empty
# environment. It speaks line-delimited JSON-RPC 2.0 on stdin/stdout (Engi
# Plugin Protocol v1, see docs/15-plugins.md): it answers one `execute` request
# and exits. stdout carries protocol messages only; user `print` output is
# forwarded as `log` notifications.
#
# Python 3.9 compatible, standard library only.
#
# This is a guard against mistakes, NOT a security boundary: Python cannot be
# sandboxed in-process.

import ast
import datetime
import decimal
import json
import sys
import traceback
import uuid

CODE_NODE_ERROR = -32001
INTERNAL_ERROR = -32603
INVALID_PARAMS = -32602
METHOD_NOT_FOUND = -32601

USER_FILE = "<engi-code>"
USER_FUNC = "_engi_main"

# Names user code may not reference.
BLOCKED_NAMES = frozenset([
    "__import__", "importlib", "eval", "exec", "compile", "open", "globals", "locals", "vars",
    "getattr", "setattr", "delattr", "breakpoint", "input",
])

# Attributes that expose interpreter frames or code objects, besides dunders.
BLOCKED_ATTRS = frozenset([
    "gi_frame", "gi_code", "cr_frame", "cr_code", "ag_frame", "ag_code",
    "f_globals", "f_locals", "f_builtins", "f_back", "f_code", "tb_frame",
])

# Builtins visible to user code. `__import__` and `__build_class__` are added
# separately (guarded import, class statements).
SAFE_BUILTIN_NAMES = (
    "abs all any ascii bin bool bytearray bytes callable chr classmethod complex dict divmod "
    "enumerate filter float format frozenset hasattr hash hex id int isinstance issubclass iter "
    "len list map max min next object oct ord pow property range repr reversed round set slice "
    "sorted staticmethod str sum super tuple type zip "
    "BaseException Exception ArithmeticError AssertionError AttributeError EOFError ImportError "
    "IndexError KeyError LookupError NameError NotImplementedError OverflowError RecursionError "
    "RuntimeError StopIteration TypeError UnicodeError ValueError ZeroDivisionError "
    "NotImplemented Ellipsis"
).split()

_protocol_out = sys.stdout
_protocol_in = sys.stdin.buffer


def send(message):
    message["jsonrpc"] = "2.0"
    _protocol_out.write(json.dumps(message, separators=(",", ":")) + "\n")
    _protocol_out.flush()


def reply(req_id, result):
    send({"id": req_id, "result": result})


def reply_error(req_id, code, message, data=None):
    err = {"code": code, "message": message}
    if data:
        err["data"] = data
    send({"id": req_id, "error": err})


def read_message():
    line = _protocol_in.readline()
    if not line:
        return None
    line = line.strip()
    if not line:
        return read_message()
    return json.loads(line.decode("utf-8"))


class UserCodeError(Exception):
    """A problem with the user's code or its result, reported as a node error."""

    def __init__(self, message, description=None, item_index=None):
        Exception.__init__(self, message)
        self.description = description
        self.item_index = item_index


class ExprError(Exception):
    """Raised in user code when the host fails to evaluate an expression."""


class LogWriter(object):
    """Replaces sys.stdout: every complete line becomes a `log` notification."""

    def __init__(self):
        self._buf = ""

    def write(self, text):
        if not isinstance(text, str):
            text = str(text)
        self._buf += text
        while "\n" in self._buf:
            line, self._buf = self._buf.split("\n", 1)
            self._emit(line)
        return len(text)

    def flush(self):
        pass

    def finish(self):
        if self._buf:
            self._emit(self._buf)
            self._buf = ""

    @staticmethod
    def _emit(line):
        send({"method": "log", "params": {"level": "info", "message": line}})


# --- static analysis ---------------------------------------------------------


def module_allowed(name, allowed):
    """An entry allows the module of that exact dotted name and its submodules."""
    for entry in allowed:
        if name == entry or name.startswith(entry + "."):
            return True
    return False


def import_allowed(module, names, allowed):
    """Checks `import module` (names empty) or `from module import names`."""
    if module and module_allowed(module, allowed):
        return True
    # `from urllib import parse` is fine when `urllib.parse` is allowed.
    if module and names and all(module_allowed(module + "." + n, allowed) for n in names if n != "*"):
        return not ("*" in names)
    return False


class Analyzer(ast.NodeVisitor):
    def __init__(self, allowed):
        self.allowed = allowed
        self.problems = []

    def problem(self, node, text):
        self.problems.append((getattr(node, "lineno", 0), text))

    def visit_Import(self, node):
        for alias in node.names:
            if not import_allowed(alias.name, [], self.allowed):
                self.problem(node, "import of '%s' is not allowed" % alias.name)
        self.generic_visit(node)

    def visit_ImportFrom(self, node):
        if node.level:
            self.problem(node, "relative imports are not allowed")
        elif not import_allowed(node.module or "", [a.name for a in node.names], self.allowed):
            self.problem(node, "import from '%s' is not allowed" % (node.module or ""))
        self.generic_visit(node)

    def visit_Name(self, node):
        if node.id in BLOCKED_NAMES or node.id.startswith("__"):
            self.problem(node, "use of '%s' is not allowed" % node.id)
        self.generic_visit(node)

    def visit_Attribute(self, node):
        if node.attr.startswith("__") or node.attr in BLOCKED_ATTRS:
            self.problem(node, "access to attribute '%s' is not allowed" % node.attr)
        self.generic_visit(node)


def analyze(tree, allowed):
    a = Analyzer(allowed)
    a.visit(tree)
    if a.problems:
        a.problems.sort(key=lambda p: p[0])
        text = "; ".join("line %d: %s" % p for p in a.problems)
        raise UserCodeError("ForbiddenCode: " + text, "The code uses features the Python Code node does not allow.")


def compile_user_code(code, allowed):
    """Parses code, checks it, and compiles it as the body of a function so that
    `return` works. Line numbers still refer to the user's code."""
    try:
        tree = ast.parse(code, filename=USER_FILE)
    except SyntaxError as e:
        raise UserCodeError("SyntaxError: %s (line %s)" % (e.msg, e.lineno), None)
    analyze(tree, allowed)
    module = ast.parse("def %s():\n    pass\n" % USER_FUNC)
    module.body[0].body = tree.body or [ast.Pass(lineno=1, col_offset=0)]
    ast.fix_missing_locations(module)
    try:
        return compile(module, USER_FILE, "exec")
    except SyntaxError as e:
        raise UserCodeError("SyntaxError: %s (line %s)" % (e.msg, e.lineno), None)


# --- execution environment ---------------------------------------------------


def make_builtins(allowed):
    import builtins as real

    safe = {name: getattr(real, name) for name in SAFE_BUILTIN_NAMES}
    safe["print"] = real.print
    safe["__build_class__"] = real.__build_class__
    safe["__name__"] = "engi_code"

    def guarded_import(name, globals=None, locals=None, fromlist=(), level=0):
        if level:
            raise ImportError("relative imports are not allowed")
        names = [n for n in (fromlist or ()) if n != "*"]
        if not import_allowed(name, names if fromlist else [], allowed):
            raise ImportError("import of '%s' is not allowed" % name)
        return real.__import__(name, globals, locals, fromlist, level)

    safe["__import__"] = guarded_import
    return safe


class HostLink(object):
    """Blocking request/response calls to the host while user code runs."""

    def __init__(self, call_id):
        self.call_id = call_id
        self._next = 0

    def eval_expression(self, expression, item_index):
        self._next += 1
        req_id = "py-%d" % self._next
        send({"id": req_id, "method": "expression.eval",
              "params": {"callId": self.call_id, "expression": expression, "itemIndex": item_index}})
        while True:
            msg = read_message()
            if msg is None:
                raise ExprError("the host closed the connection")
            if msg.get("method") or msg.get("id") != req_id:
                continue  # notifications such as $/cancel; the host kills us on timeout
            if msg.get("error"):
                raise ExprError(msg["error"].get("message", "expression failed"))
            return (msg.get("result") or {}).get("value")


def json_default(value):
    if isinstance(value, (datetime.datetime, datetime.date, datetime.time)):
        return value.isoformat()
    if isinstance(value, decimal.Decimal):
        return float(value)
    if isinstance(value, uuid.UUID):
        return str(value)
    if isinstance(value, (set, frozenset, tuple)):
        return list(value)
    raise TypeError("%s is not JSON serializable" % type(value).__name__)


def type_name(value):
    return "null" if value is None else type(value).__name__


def to_item(value, position):
    if not isinstance(value, dict):
        raise UserCodeError("returned item %d must be a dict, got %s" % (position, type_name(value)))
    inner = value.get("json")
    if isinstance(inner, dict):
        item = {"json": inner}
        binary = value.get("binary")
        if isinstance(binary, dict) and binary:
            item["binary"] = binary
        return item
    return {"json": value}


def to_items(value):
    """None -> []; a dict or a list/tuple of dicts -> a list of wire items."""
    if value is None:
        return []
    if isinstance(value, (list, tuple)):
        return [to_item(v, i) for i, v in enumerate(value)]
    return [to_item(value, 0)]


def run_execute(params):
    allowed = list(params.get("allowedModules") or [])
    mode = params.get("mode")
    if mode not in ("runOnceForAllItems", "runOnceForEachItem"):
        raise UserCodeError("unknown mode %r" % (mode,))
    max_bytes = params.get("maxOutputBytes") or 0
    compiled = compile_user_code(params.get("code") or "", allowed)
    source_lines = (params.get("code") or "").splitlines()

    items = [{"json": it.get("json") or {}, "binary": it.get("binary") or {}} for it in params.get("items") or []]
    info = params.get("execution") or {}
    link = HostLink(params.get("callId", ""))
    state = {"index": 0}

    def expr(expression, item_index=None):
        return link.eval_expression(expression, state["index"] if item_index is None else item_index)

    scope = {
        "__builtins__": make_builtins(allowed),
        "__name__": "engi_code",
        # json is the first item's data in all-items mode (as $json is in the
        # JavaScript node), an empty dict with no input; item is per-item only.
        "items": items, "item": None, "json": items[0]["json"] if items else {}, "item_index": 0,
        "run_index": info.get("runIndex", 0),
        "execution": {"id": info.get("executionId", ""), "mode": info.get("mode", "")},
        "workflow": {"id": info.get("workflowId", ""), "name": info.get("workflowName", "")},
        "expr": expr, "ExprError": ExprError,
    }
    exec(compiled, scope)
    main = scope[USER_FUNC]

    def call(index):
        try:
            return main()
        except UserCodeError:
            raise
        except BaseException as e:  # noqa: B902 - user code may raise anything
            if isinstance(e, KeyboardInterrupt):
                raise
            raise UserCodeError(
                "%s: %s" % (type(e).__name__, e), user_traceback(e, source_lines), index)

    if mode == "runOnceForAllItems":
        result = {"items": to_items_checked(call(None), None)}
    else:
        per_item = []
        for i, it in enumerate(items):
            state["index"] = i
            scope["item"], scope["json"], scope["item_index"] = it, it["json"], i
            value = call(i)
            try:
                produced = to_items(value)
            except UserCodeError as e:
                raise UserCodeError(str(e), e.description, i)
            per_item.append(produced or None)
        result = {"perItem": per_item}

    size = len(json.dumps(result, default=json_default, allow_nan=False, separators=(",", ":")))
    if max_bytes and size > max_bytes:
        raise UserCodeError("the code returned %d bytes, the limit is %d" % (size, max_bytes))
    return json.loads(json.dumps(result, default=json_default, allow_nan=False))


def to_items_checked(value, index):
    try:
        return to_items(value)
    except UserCodeError as e:
        raise UserCodeError(str(e), e.description, index)


def user_traceback(exc, source_lines):
    lines = []
    for frame in traceback.extract_tb(exc.__traceback__):
        if frame.filename != USER_FILE:
            continue
        text = source_lines[frame.lineno - 1].strip() if 0 < frame.lineno <= len(source_lines) else ""
        where = "" if frame.name == USER_FUNC else ", in %s" % frame.name
        lines.append("line %d%s: %s" % (frame.lineno, where, text))
    return "\n".join(lines)


def handle(msg):
    method = msg.get("method")
    req_id = msg.get("id")
    if method == "shutdown":
        if req_id is not None:
            reply(req_id, {})
        return True
    if method != "execute":
        if req_id is not None:
            reply_error(req_id, METHOD_NOT_FOUND, "unknown method %r" % (method,))
        return False
    writer = LogWriter()
    real_stdout = sys.stdout
    sys.stdout = writer
    try:
        result = run_execute(msg.get("params") or {})
    except UserCodeError as e:
        data = {}
        if e.description:
            data["description"] = e.description
        if e.item_index is not None:
            data["itemIndex"] = e.item_index
        writer.finish()
        sys.stdout = real_stdout
        reply_error(req_id, CODE_NODE_ERROR, str(e), data)
        return True
    except BaseException as e:  # noqa: B902 - last resort, keeps the protocol intact
        writer.finish()
        sys.stdout = real_stdout
        reply_error(req_id, INTERNAL_ERROR, "runner failure: %s: %s" % (type(e).__name__, e))
        return True
    writer.finish()
    sys.stdout = real_stdout
    reply(req_id, result)
    return True


def main():
    while True:
        try:
            msg = read_message()
        except ValueError:
            send({"id": None, "error": {"code": -32700, "message": "invalid JSON"}})
            continue
        if msg is None or handle(msg):
            return


main()
