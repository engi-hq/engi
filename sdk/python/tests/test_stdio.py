import io
import json
import sys
import threading
import unittest
from contextlib import redirect_stderr

from engi_plugin import (
    CredentialRef, Descriptor, DescriptorError, MessageTooLargeError, NodeError, Output, Plugin, Port,
    Property, Wait, check_descriptor,
)

from helpers import FakeHost, execute_params


def build_plugin(**opts) -> Plugin:
    plugin = Plugin(name="t", version="0.0.1", **opts)

    @plugin.node(type="acme.text.reverse", display_name="Reverse", group="transform",
                 properties=[Property("field", default="text", required=True)],
                 credentials=[CredentialRef("api", "acme.apiKey")])
    def reverse(ctx):
        out = []
        for i, item in enumerate(ctx.items()):
            f = ctx.param("field", i)
            out.append({"json": {"reversed": item["json"][f][::-1]}, "pairedItem": [{"input": 0, "item": i}]})
        return [out]

    @plugin.node(type="acme.test.fail", group="action")
    def fail(ctx):
        raise NodeError("boom", description="why", http_status=502, item_index=1)

    @plugin.node(type="acme.test.crash")
    def crash(ctx):
        raise KeyError("x")

    @plugin.node(type="acme.test.blob")
    def blob(ctx):
        ref = ctx.blob.write("a.txt", "text/plain", b"hello")
        data = ctx.blob.read(ref)
        value = ctx.eval("1+1", 2)
        return [[{"json": {"data": data.decode(), "value": value, "ref": ref}}]]

    @plugin.node(type="acme.test.sleep")
    def sleep(ctx):
        ctx.log.info("started", extra={"attrs": {"k": 1}})
        ctx.cancelled.wait(5)
        return [[{"json": {"cancelled": ctx.cancelled.is_set()}}]]

    @plugin.node(type="acme.test.logs")
    def logs(ctx):
        ctx.log.info("started", extra={"attrs": {"k": 1}})
        return [[{"json": {}}]]

    @plugin.node(type="acme.test.noisy")
    def noisy(ctx):
        print("stray output")
        return [[{"json": {}}]]

    @plugin.node(type="acme.test.wait")
    def wait(ctx):
        return Output([[{"json": {}}]], Wait(resume_token="tok"))

    @plugin.node(type="acme.test.cred")
    def cred(ctx):
        return [[{"json": {"key": ctx.credential("api")["key"]}}]]

    return plugin


class StdioTest(unittest.TestCase):
    def setUp(self):
        self.host = None

    def tearDown(self):
        if self.host:
            self.host.close()

    def start(self, plugin=None, on_request=None) -> FakeHost:
        self.host = FakeHost(plugin or build_plugin(), on_request)
        return self.host

    def test_initialize_and_descriptors(self):
        h = self.start()
        res = h.call("initialize", {"protocolVersion": 1, "host": {"name": "engi", "version": "x"}})["result"]
        self.assertEqual(res["protocolVersion"], 1)
        self.assertEqual(res["plugin"], {"name": "t", "version": "0.0.1"})
        d = next(n for n in res["nodes"] if n["type"] == "acme.text.reverse")
        self.assertEqual(d["displayName"], "Reverse")
        self.assertEqual(d["group"], "transform")
        self.assertEqual(d["inputs"], [{"name": "main"}])
        self.assertEqual(d["properties"], [{"name": "field", "type": "string", "required": True, "default": "text"}])
        self.assertEqual(d["credentials"], [{"name": "api", "type": "acme.apiKey"}])
        self.assertEqual(h.call("health")["result"], {})

    def test_negotiates_down_from_newer_host(self):
        h = self.start()
        res = h.call("initialize", {"protocolVersion": 7})["result"]
        self.assertEqual(res["protocolVersion"], 1)
        self.assertIn("error", h.call("initialize", {"protocolVersion": 0}))

    def test_execute_reverse(self):
        h = self.start()
        r = h.call("execute", execute_params("acme.text.reverse", [{"json": {"text": "abc"}}], [{"field": "text"}]))
        self.assertEqual(r["result"]["outputs"], [[{"json": {"reversed": "cba"}, "pairedItem": [{"input": 0, "item": 0}]}]])

    def test_param_error_raised_when_read(self):
        h = self.start()
        p = execute_params("acme.text.reverse", [{"json": {"text": "a"}}], [{}], paramErrors=[{"field": "expression failed"}])
        err = h.call("execute", p)["error"]
        self.assertEqual(err["code"], -32001)
        self.assertEqual(err["message"], "expression failed")
        self.assertEqual(err["data"], {"itemIndex": 0})

    def test_node_error_mapping(self):
        h = self.start()
        err = h.call("execute", execute_params("acme.test.fail"))["error"]
        self.assertEqual(err["code"], -32001)
        self.assertEqual(err["message"], "boom")
        self.assertEqual(err["data"], {"description": "why", "httpStatus": 502, "itemIndex": 1})

    def test_unexpected_exception_is_internal_error(self):
        h = self.start()
        err = h.call("execute", execute_params("acme.test.crash"))["error"]
        self.assertEqual(err["code"], -32603)
        self.assertIn("KeyError", err["message"])
        self.assertEqual(h.wait_notification("log")["params"]["level"], "error")

    def test_unknown_node_and_method(self):
        h = self.start()
        self.assertEqual(h.call("execute", execute_params("acme.no.such"))["error"]["code"], -32602)
        self.assertEqual(h.call("nope")["error"]["code"], -32601)

    def test_credentials(self):
        h = self.start()
        ok = execute_params("acme.test.cred", credentials={"api": {"type": "acme.apiKey", "id": "c", "version": 1, "data": {"key": "k1"}}})
        self.assertEqual(h.call("execute", ok)["result"]["outputs"][0][0]["json"], {"key": "k1"})
        bad = execute_params("acme.test.cred", credentialErrors={"api": "cannot decrypt"})
        self.assertEqual(h.call("execute", bad)["error"]["message"], "cannot decrypt")
        missing = h.call("execute", execute_params("acme.test.cred"))["error"]
        self.assertIn("not bound", missing["message"])

    def test_wait_result(self):
        h = self.start()
        r = h.call("execute", execute_params("acme.test.wait"))["result"]
        self.assertEqual(r["wait"], {"resumeToken": "tok"})

    def test_callbacks_round_trip(self):
        seen = []

        def on_request(msg):
            seen.append(msg)
            if msg["method"] == "blob.write":
                return {"result": {"ref": "blob_1", "mime": msg["params"]["mime"], "name": msg["params"]["name"], "size": 5}}
            if msg["method"] == "blob.read":
                return {"result": {"data": "aGVsbG8="}}
            return {"result": {"value": 2}}

        h = self.start(on_request=on_request)
        r = h.call("execute", execute_params("acme.test.blob"))["result"]
        j = r["outputs"][0][0]["json"]
        self.assertEqual((j["data"], j["value"], j["ref"]["ref"]), ("hello", 2, "blob_1"))
        self.assertTrue(all(m["params"]["callId"] == "c1" for m in seen))
        self.assertEqual(seen[0]["params"]["data"], "aGVsbG8=")
        self.assertEqual(seen[2]["params"], {"expression": "1+1", "itemIndex": 2, "callId": "c1"})

    def test_node_logs_carry_the_call_id(self):
        h = self.start()
        h.call("execute", execute_params("acme.test.logs"))
        log = h.wait_notification("log")
        self.assertEqual(log["params"], {"level": "info", "message": "started", "attrs": {"k": 1}, "callId": "c1"})

    def test_callback_error_fails_node(self):
        h = self.start(on_request=lambda m: {"error": {"code": -32001, "message": "no such blob"}})
        err = h.call("execute", execute_params("acme.test.blob"))["error"]
        self.assertEqual((err["code"], err["message"]), (-32001, "no such blob"))

    def test_cancel(self):
        h = self.start()
        rid = h.start("execute", execute_params("acme.test.sleep"))
        h.wait_notification("log")
        h.send({"jsonrpc": "2.0", "method": "$/cancel", "params": {"id": rid}})
        r = h.wait(rid)
        self.assertEqual(r["result"]["outputs"][0][0]["json"], {"cancelled": True})
        n = next(x for x in h.notifications if x["method"] == "log")
        self.assertEqual(n["params"], {"level": "info", "message": "started", "attrs": {"k": 1}, "callId": "c1"})

    def test_cancel_unblocks_pending_callback(self):
        gate = threading.Event()

        def on_request(msg):
            gate.wait(5)
            return {"result": {"value": 1}}

        h = self.start(on_request=on_request)
        rid = h.start("execute", execute_params("acme.test.blob"))
        h.send({"jsonrpc": "2.0", "method": "$/cancel", "params": {"id": rid}})
        err = h.wait(rid)["error"]
        gate.set()
        self.assertEqual(err["code"], -32002)

    def test_shutdown_waits_for_in_flight(self):
        h = self.start()
        rid = h.start("execute", execute_params("acme.test.sleep"))
        h.wait_notification("log")
        h.send({"jsonrpc": "2.0", "method": "shutdown", "params": {}})
        self.assertFalse(h._serve.join(0.2) or not h._serve.is_alive())
        h.send({"jsonrpc": "2.0", "method": "$/cancel", "params": {"id": rid}})
        h.wait(rid)
        h._serve.join(5)
        self.assertFalse(h._serve.is_alive())
        self.assertIsNone(h.error)

    def test_busy_above_cap(self):
        h = self.start(build_plugin(max_concurrent_calls=1))
        first = h.start("execute", execute_params("acme.test.sleep"))
        h.wait_notification("log")
        err = h.call("execute", execute_params("acme.test.reverse"))["error"]
        self.assertEqual(err["code"], -32004)
        h.send({"jsonrpc": "2.0", "method": "$/cancel", "params": {"id": first}})
        h.wait(first)

    def test_oversize_message_closes_connection(self):
        h = self.start(build_plugin(max_message_bytes=512))
        h.send_raw(b'{"jsonrpc":"2.0","id":1,"method":"health","params":{"pad":"' + b"x" * 1024 + b'"}}\n')
        h._serve.join(5)
        self.assertIsInstance(h.error, MessageTooLargeError)

    def test_oversize_result_becomes_error(self):
        plugin = Plugin("t", "1", max_message_bytes=2048)

        @plugin.node(type="acme.test.big")
        def big(ctx):
            return [[{"json": {"s": "x" * 5000}}]]

        h = self.start(plugin)
        self.assertEqual(h.call("execute", execute_params("acme.test.big"))["error"]["code"], -32603)

    def test_stray_print_does_not_corrupt_stdout(self):
        real_stdout = sys.stdout
        h = self.start()
        with redirect_stderr(io.StringIO()):
            r = h.call("execute", execute_params("acme.test.noisy"))
        self.assertEqual(r["result"]["outputs"], [[{"json": {}}]])
        for line in h.raw_lines:
            self.assertEqual(json.loads(line)["jsonrpc"], "2.0")
        h.close()
        self.assertIs(sys.stdout, real_stdout)

    def test_garbage_line_gets_parse_error(self):
        h = self.start()
        h.send_raw(b"not json\n")
        with h._cv:
            h._cv.wait_for(lambda: None in h.responses, 5)
        self.assertEqual(h.responses[None]["error"]["code"], -32700)
        self.assertEqual(h.call("health")["result"], {})


class ValidationTest(unittest.TestCase):
    def test_check_descriptor_rules(self):
        self.assertEqual(check_descriptor(Descriptor(type="acme.a.b", group="action")), [])
        bad = Descriptor(type="engi.http", group="trigger", inputs=[Port("m", kind="ai.model")])
        msgs = " | ".join(check_descriptor(bad))
        self.assertIn("reserved", msgs)
        self.assertIn("group", msgs)
        self.assertIn("main ports", msgs)
        self.assertTrue(check_descriptor(Descriptor(type="nodots")))
        self.assertTrue(check_descriptor(Descriptor(type="a.b", properties=[Property("x"), Property("x")])))
        self.assertTrue(check_descriptor(Descriptor(type="a.b", properties=[Property("x", type="bogus")])))

    def test_serve_rejects_invalid_descriptor(self):
        plugin = Plugin("t", "1")
        plugin.node(type="engi.mine")(lambda ctx: [[]])
        with self.assertRaises(DescriptorError):
            plugin.serve(io.BytesIO(), io.BytesIO())
        with self.assertRaises(DescriptorError):
            Plugin("t", "1").serve(io.BytesIO(), io.BytesIO())

    def test_duplicate_registration(self):
        plugin = Plugin("t", "1")
        plugin.node(type="a.b")(lambda ctx: [[]])
        with self.assertRaises(DescriptorError):
            plugin.node(type="a.b")(lambda ctx: [[]])

    def test_eof_ends_serve(self):
        plugin = build_plugin()
        out = io.BytesIO()
        plugin.serve(io.BytesIO(b'{"jsonrpc":"2.0","id":1,"method":"health"}\n'), out)
        self.assertEqual(json.loads(out.getvalue()), {"jsonrpc": "2.0", "id": 1, "result": {}})


if __name__ == "__main__":
    unittest.main()
