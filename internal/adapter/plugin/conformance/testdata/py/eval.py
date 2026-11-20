"""Conformance test plugin: calls the host's expression.eval for every item."""

import os
import sys

# testdata/py -> repository root is six levels up.
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), *[".."] * 6, "sdk", "python"))

from engi_plugin import Plugin  # noqa: E402

plugin = Plugin(name="conformance-py-eval", version="0.1.0")


@plugin.node(type="conformance.python.eval", display_name="Eval", group="transform")
def run(ctx):
    ctx.log.info("evaluating", extra={"attrs": {"step": "eval"}})
    return [[{"json": {"value": ctx.eval("1+1", i)}} for i, _ in enumerate(ctx.items())]]


if __name__ == "__main__":
    plugin.serve()
