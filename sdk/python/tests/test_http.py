import json
import threading
import unittest
import urllib.error
import urllib.request

from test_stdio import build_plugin
from helpers import execute_params


class HttpTest(unittest.TestCase):
    def setUp(self):
        self.server = build_plugin().http_server("127.0.0.1", 0, token="s3cret")
        self.url = "http://127.0.0.1:%d/rpc" % self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def post(self, body, token="s3cret", url=None):
        req = urllib.request.Request(url or self.url, data=json.dumps(body).encode(), method="POST")
        if token:
            req.add_header("Authorization", "Bearer " + token)
        with urllib.request.urlopen(req) as r:
            data = r.read()
            return r.status, json.loads(data) if data else None

    def rpc(self, method, params, id=1):
        return self.post({"jsonrpc": "2.0", "id": id, "method": method, "params": params})[1]

    def test_initialize_and_execute(self):
        res = self.rpc("initialize", {"protocolVersion": 1})
        self.assertEqual(res["result"]["plugin"]["name"], "t")
        r = self.rpc("execute", execute_params("acme.text.reverse", [{"json": {"text": "ab"}}], [{"field": "text"}]), id="x")
        self.assertEqual(r["id"], "x")
        self.assertEqual(r["result"]["outputs"][0][0]["json"], {"reversed": "ba"})

    def test_node_error(self):
        err = self.rpc("execute", execute_params("acme.test.fail"))["error"]
        self.assertEqual((err["code"], err["data"]["httpStatus"]), (-32001, 502))

    def test_callbacks_unsupported(self):
        err = self.rpc("execute", execute_params("acme.test.blob"))["error"]
        self.assertEqual(err["code"], -32003)
        self.assertIn("not supported", err["message"])

    def test_notification_is_204(self):
        self.assertEqual(self.post({"jsonrpc": "2.0", "method": "shutdown", "params": {}})[0], 204)

    def test_auth_path_and_method(self):
        with self.assertRaises(urllib.error.HTTPError) as cm:
            self.post({"jsonrpc": "2.0", "id": 1, "method": "health"}, token="wrong")
        self.assertEqual(cm.exception.code, 401)
        with self.assertRaises(urllib.error.HTTPError) as cm:
            self.post({"jsonrpc": "2.0", "id": 1, "method": "health"}, url=self.url.replace("/rpc", "/other"))
        self.assertEqual(cm.exception.code, 404)
        with self.assertRaises(urllib.error.HTTPError) as cm:
            urllib.request.urlopen(self.url)
        self.assertEqual(cm.exception.code, 405)

    def test_oversize_body(self):
        server = build_plugin(max_message_bytes=256).http_server("127.0.0.1", 0)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            with self.assertRaises(urllib.error.HTTPError) as cm:
                self.post({"jsonrpc": "2.0", "id": 1, "method": "health", "pad": "x" * 1000}, token=None,
                          url="http://127.0.0.1:%d/rpc" % server.server_address[1])
            self.assertEqual(cm.exception.code, 413)
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    unittest.main()
