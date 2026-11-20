import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { afterEach, describe, it } from "node:test";
import {
  checkDescriptor, definePlugin, DescriptorError, MessageTooLargeError, normalizeDefinition,
} from "../index.js";
import { buildPlugin, executeParams, FakeHost } from "./helpers.js";

describe("stdio transport", () => {
  let host: FakeHost | undefined;
  const start = (...args: ConstructorParameters<typeof FakeHost>): FakeHost => (host = new FakeHost(...args));
  afterEach(async () => {
    await host?.close();
    host = undefined;
  });

  it("initializes and describes nodes", async () => {
    const h = start(buildPlugin());
    const res = (await h.call("initialize", { protocolVersion: 1, host: { name: "engi", version: "x" } })).result;
    assert.equal(res.protocolVersion, 1);
    assert.deepEqual(res.plugin, { name: "t", version: "0.0.1" });
    const d = res.nodes.find((n: any) => n.type === "acme.text.reverse");
    assert.equal(d.displayName, "Reverse");
    assert.equal(d.group, "transform");
    assert.deepEqual(d.inputs, [{ name: "main" }]);
    assert.deepEqual(d.properties, [{ name: "field", type: "string", required: true, default: "text" }]);
    assert.deepEqual(d.credentials, [{ name: "api", type: "acme.apiKey" }]);
    assert.deepEqual((await h.call("health")).result, {});
  });

  it("negotiates the protocol version", async () => {
    const h = start(buildPlugin());
    assert.equal((await h.call("initialize", { protocolVersion: 7 })).result.protocolVersion, 1);
    assert.ok((await h.call("initialize", { protocolVersion: 0 })).error);
  });

  it("executes a node", async () => {
    const h = start(buildPlugin());
    const r = await h.call("execute", executeParams("acme.text.reverse", [{ json: { text: "abc" } }], [{ field: "text" }]));
    assert.deepEqual(r.result.outputs, [[{ json: { reversed: "cba" }, pairedItem: [{ input: 0, item: 0 }] }]]);
  });

  it("raises the host's message when a failed parameter is read", async () => {
    const h = start(buildPlugin());
    const p = executeParams("acme.text.reverse", [{ json: { text: "a" } }], [{}], { paramErrors: [{ field: "expression failed" }] });
    const err = (await h.call("execute", p)).error;
    assert.deepEqual([err.code, err.message, err.data], [-32001, "expression failed", { itemIndex: 0 }]);
  });

  it("maps NodeError and other exceptions", async () => {
    const h = start(buildPlugin());
    const err = (await h.call("execute", executeParams("acme.test.fail"))).error;
    assert.deepEqual([err.code, err.message], [-32001, "boom"]);
    assert.deepEqual(err.data, { description: "why", httpStatus: 502, itemIndex: 1 });
    const crash = (await h.call("execute", executeParams("acme.test.crash"))).error;
    assert.equal(crash.code, -32603);
    assert.match(crash.message, /TypeError/);
    assert.equal((await h.notification("log")).params.level, "error");
  });

  it("rejects unknown nodes and methods", async () => {
    const h = start(buildPlugin());
    assert.equal((await h.call("execute", executeParams("acme.no.such"))).error.code, -32602);
    assert.equal((await h.call("nope")).error.code, -32601);
  });

  it("serves credentials and their errors", async () => {
    const h = start(buildPlugin());
    const ok = executeParams("acme.test.cred", [], [{}], { credentials: { api: { type: "acme.apiKey", id: "c", version: 1, data: { key: "k1" } } } });
    assert.equal((await h.call("execute", ok)).result.outputs[0][0].json.key, "k1");
    const bad = executeParams("acme.test.cred", [], [{}], { credentialErrors: { api: "cannot decrypt" } });
    assert.equal((await h.call("execute", bad)).error.message, "cannot decrypt");
    assert.match((await h.call("execute", executeParams("acme.test.cred"))).error.message, /not bound/);
  });

  it("returns a wait", async () => {
    const h = start(buildPlugin());
    assert.deepEqual((await h.call("execute", executeParams("acme.test.wait"))).result.wait, { resumeToken: "tok" });
  });

  it("round-trips callbacks", async () => {
    const seen: any[] = [];
    const h = start(buildPlugin(), (m) => {
      seen.push(m);
      if (m.method === "blob.write") return { result: { ref: "blob_1", mime: m.params.mime, name: m.params.name, size: 5 } };
      if (m.method === "blob.read") return { result: { data: "aGVsbG8=" } };
      return { result: { value: 2 } };
    });
    const j = (await h.call("execute", executeParams("acme.test.blob"))).result.outputs[0][0].json;
    assert.deepEqual([j.data, j.value, j.ref.ref], ["hello", 2, "blob_1"]);
    assert.ok(seen.every((m) => m.params.callId === "c1"));
    assert.equal(seen[0].params.data, "aGVsbG8=");
    assert.deepEqual(seen[2].params, { expression: "1+1", itemIndex: 2, callId: "c1" });
  });

  it("fails the node when a callback errors", async () => {
    const h = start(buildPlugin(), () => ({ error: { code: -32001, message: "no such blob" } }));
    const err = (await h.call("execute", executeParams("acme.test.blob"))).error;
    assert.deepEqual([err.code, err.message], [-32001, "no such blob"]);
  });

  it("cancels a call", async () => {
    const h = start(buildPlugin());
    const id = h.start("execute", executeParams("acme.test.sleep"));
    const log = await h.notification("log");
    assert.deepEqual(log.params, { level: "info", message: "started", attrs: { k: 1 }, callId: "c1" });
    h.send({ jsonrpc: "2.0", method: "$/cancel", params: { id } });
    assert.deepEqual((await h.wait(id)).result.outputs[0][0].json, { cancelled: true });
  });

  it("cancel unblocks a pending callback", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const h = start(buildPlugin(), async () => {
      await gate;
      return { result: { value: 1 } };
    });
    const id = h.start("execute", executeParams("acme.test.blob"));
    await new Promise((r) => setTimeout(r, 50));
    h.send({ jsonrpc: "2.0", method: "$/cancel", params: { id } });
    const err = (await h.wait(id)).error;
    release();
    assert.equal(err.code, -32002);
  });

  it("shutdown waits for in-flight work", async () => {
    const h = start(buildPlugin());
    const id = h.start("execute", executeParams("acme.test.sleep"));
    await h.notification("log");
    h.send({ jsonrpc: "2.0", method: "shutdown", params: {} });
    let finished = false;
    void h.served.then(() => (finished = true));
    await new Promise((r) => setTimeout(r, 100));
    assert.equal(finished, false);
    h.send({ jsonrpc: "2.0", method: "$/cancel", params: { id } });
    await h.wait(id);
    await h.served;
    assert.equal(h.error, undefined);
  });

  it("answers busy above the concurrency cap", async () => {
    const h = start(buildPlugin({ maxConcurrentCalls: 1 }));
    const first = h.start("execute", executeParams("acme.test.sleep"));
    await h.notification("log");
    assert.equal((await h.call("execute", executeParams("acme.text.reverse"))).error.code, -32004);
    h.send({ jsonrpc: "2.0", method: "$/cancel", params: { id: first } });
    await h.wait(first);
  });

  it("closes the connection on an oversize message", async () => {
    const h = start(buildPlugin({ maxMessageBytes: 512 }));
    h.toPlugin.write('{"jsonrpc":"2.0","id":1,"method":"health","params":{"pad":"' + "x".repeat(1024) + '"}}\n');
    await h.served;
    assert.ok(h.error instanceof MessageTooLargeError);
  });

  it("turns an oversize result into an error", async () => {
    const h = start(buildPlugin({ maxMessageBytes: 2048 }));
    assert.equal((await h.call("execute", executeParams("acme.test.big"))).error.code, -32603);
  });

  it("keeps stray console.log off the protocol stream", async () => {
    const h = start(buildPlugin());
    const realLog = console.log;
    const written: string[] = [];
    const realErr = process.stderr.write;
    process.stderr.write = ((c: any) => (written.push(String(c)), true)) as any;
    try {
      const r = await h.call("execute", executeParams("acme.test.noisy"));
      assert.deepEqual(r.result.outputs, [[{ json: {} }]]);
    } finally {
      process.stderr.write = realErr;
    }
    assert.ok(written.some((w) => w.includes("stray output")));
    for (const line of h.rawLines) assert.equal(JSON.parse(line).jsonrpc, "2.0");
    await h.close();
    assert.equal(console.log, realLog);
  });

  it("answers garbage with a parse error and keeps serving", async () => {
    const h = start(buildPlugin());
    h.toPlugin.write("not json\n");
    await h.until(() => h.responses.has(null), "parse error");
    assert.equal(h.responses.get(null)!.error.code, -32700);
    assert.deepEqual((await h.call("health")).result, {});
  });

  it("ends serve at end of input", async () => {
    const input = new PassThrough();
    const output = new PassThrough();
    const chunks: Buffer[] = [];
    output.on("data", (c) => chunks.push(c));
    input.end('{"jsonrpc":"2.0","id":1,"method":"health"}\n');
    await buildPlugin().serve({ input, output });
    assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { jsonrpc: "2.0", id: 1, result: {} });
  });
});

describe("descriptor validation", () => {
  it("applies the v1 rules", () => {
    assert.deepEqual(checkDescriptor(normalizeDefinition({ type: "acme.a.b" })), []);
    const msgs = checkDescriptor(
      normalizeDefinition({ type: "engi.http", group: "trigger" as any, inputs: [{ name: "m", kind: "ai.model" }] }),
    ).join(" | ");
    assert.match(msgs, /reserved/);
    assert.match(msgs, /group/);
    assert.match(msgs, /main ports/);
    assert.ok(checkDescriptor(normalizeDefinition({ type: "nodots" })).length);
    assert.ok(checkDescriptor(normalizeDefinition({ type: "a.b", properties: [{ name: "x", type: "string" }, { name: "x", type: "string" }] })).length);
    assert.ok(checkDescriptor(normalizeDefinition({ type: "a.b", properties: [{ name: "x", type: "bogus" as any }] })).length);
  });

  it("serve rejects invalid descriptors and duplicates", async () => {
    const p = definePlugin({ name: "t", version: "1" }).node({ type: "engi.mine" }, () => [[]]);
    await assert.rejects(p.serve({ input: new PassThrough(), output: new PassThrough() }), DescriptorError);
    await assert.rejects(definePlugin({ name: "t", version: "1" }).serve({ input: new PassThrough(), output: new PassThrough() }), DescriptorError);
    assert.throws(() => definePlugin({ name: "t", version: "1" }).node({ type: "a.b" }, () => [[]]).node({ type: "a.b" }, () => [[]]), DescriptorError);
  });
});
