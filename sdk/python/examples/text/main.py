"""Example Engi plugin: reverses a string field.

Run from a checkout of the SDK without installing it. For a real plugin, install the
SDK instead (`pip install engi-plugin`) and delete the sys.path lines below.
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))

from engi_plugin import NodeError, Plugin, Property  # noqa: E402

plugin = Plugin(name="example-python-text", version="0.1.0")


@plugin.node(
    type="example.python.reverse",
    display_name="Reverse Text",
    group="transform",
    description="Reverses the text of a field of each item.",
    properties=[
        Property("field", display_name="Field", default="text", required=True,
                 description="Name of the string field to reverse."),
    ],
)
def reverse(ctx):
    out = []
    for i, item in enumerate(ctx.items()):
        field = ctx.param("field", i)
        value = item["json"].get(field)
        if not isinstance(value, str):
            raise NodeError("field %r is not a string" % field, item_index=i)
        out.append({"json": {"reversed": value[::-1]}, "pairedItem": [{"input": 0, "item": i}]})
    return [out]


if __name__ == "__main__":
    plugin.serve()
