import assert from "node:assert/strict";
import type { AddressInfo } from "node:net";
import type { Server } from "node:http";
import { after, before, describe, it } from "node:test";
import { buildPlugin, executeParams } from "./helpers.js";

describe("http transport", () => {
  let server: Server;
  let url: string;
  before(async () => {
    server = await buildPlugin().serveHttp({ port: 0, host: "127.0.0.1", token: "s3cret" });
    url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });
  after(() => new Promise<void>((r) => server.close(() => r())));

  const post = (body: unknown, token: string | null = "s3cret", path = "/rpc") =>
    fetch(url + path, {
      method: "POST",
      body: typeof body === "string" ? body : JSON.stringify(body),
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
  const rpc = async (method: string, params: unknown, id: string | number = 1) =>
    (await post({ jsonrpc: "2.0", id, method, params })).json() as Promise<any>;

  it("initializes and executes", async () => {
    assert.equal((await rpc("initialize", { protocolVersion: 1 })).result.plugin.name, "t");
    const r = await rpc("execute", executeParams("acme.text.reverse", [{ json: { text: "ab" } }], [{ field: "text" }]), "x");
    assert.equal(r.id, "x");
    assert.deepEqual(r.result.outputs[0][0].json, { reversed: "ba" });
  });

  it("maps node errors", async () => {
    const err = (await rpc("execute", executeParams("acme.test.fail"))).error;
    assert.deepEqual([err.code, err.data.httpStatus], [-32001, 502]);
  });

  it("rejects callbacks as unsupported", async () => {
    const err = (await rpc("execute", executeParams("acme.test.blob"))).error;
    assert.equal(err.code, -32003);
    assert.match(err.message, /not supported/);
  });

  it("answers notifications with 204", async () => {
    assert.equal((await post({ jsonrpc: "2.0", method: "shutdown", params: {} })).status, 204);
  });

  it("checks auth, path and method", async () => {
    assert.equal((await post({ jsonrpc: "2.0", id: 1, method: "health" }, "wrong")).status, 401);
    assert.equal((await post({}, "s3cret", "/other")).status, 404);
    assert.equal((await fetch(url + "/rpc", { headers: { Authorization: "Bearer s3cret" } })).status, 405);
  });

  it("rejects an oversize body", async () => {
    const small = await buildPlugin({ maxMessageBytes: 256 }).serveHttp({ port: 0, host: "127.0.0.1" });
    try {
      const res = await fetch(`http://127.0.0.1:${(small.address() as AddressInfo).port}/rpc`, {
        method: "POST",
        body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "health", pad: "x".repeat(1000) }),
      });
      assert.equal(res.status, 413);
    } finally {
      await new Promise<void>((r) => small.close(() => r()));
    }
  });
});
