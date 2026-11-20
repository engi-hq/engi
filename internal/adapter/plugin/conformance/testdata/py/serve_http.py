"""Serves the Python example plugin over HTTP on a free port and prints the port.

Usage: serve_http.py <example dir>. The example's own `plugin.serve()` sits behind
`if __name__ == "__main__"`, so importing it only registers the node.
"""

import os
import sys

example = os.path.abspath(sys.argv[1])
sys.path.insert(0, example)

import main  # noqa: E402  (the example's main.py)

server = main.plugin.http_server("127.0.0.1", 0)
print(server.server_address[1], flush=True)
try:
    server.serve_forever()
finally:
    server.server_close()
