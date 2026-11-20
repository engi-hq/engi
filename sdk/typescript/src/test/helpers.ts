import { PassThrough } from "node:stream";
import { definePlugin, NodeError, type Plugin, type PluginInfo } from "../index.js";
import type { Item } from "../index.js";

type Msg = Record<string, any>;

export function buildPlugin(extra: Partial<PluginInfo> = {}): Plugin {
  const plugin = definePlugin({ name: "t", version: "0.0.1", ...extra });
  plugin
    .node(
      {
        type: "acme.text.reverse",
        displayName: "Reverse",
        group: "transform",
        properties: [{ name: "field", type: "string", default: "text", required: true }],
        credentials: [{ name: "api", type: "acme.apiKey" }],
      },
      (ctx) => [
        ctx.items().map((item, i): Item => ({
          json: { reversed: [...String(item.json[ctx.param<string>("field", i)!])].reverse().join("") },
          pairedItem: [{ input: 0, item: i }],
        })),
      ],
    )
    .node({ type: "acme.test.fail" }, () => {
      throw new NodeError("boom", { description: "why", httpStatus: 502, itemIndex: 1 });
    })
    .node({ type: "acme.test.crash" }, () => {
      throw new TypeError("x");
    })
    .node({ type: "acme.test.blob" }, async (ctx) => {
      const ref = await ctx.blob.write("a.txt", "text/plain", new TextEncoder().encode("hello"));
      const data = await ctx.blob.read(ref);
      const value = await ctx.eval("1+1", 2);
      return [[{ json: { data: new TextDecoder().decode(data), value, ref } }]];
    })
    .node({ type: "acme.test.sleep" }, async (ctx) => {
      ctx.log.info("started", { k: 1 });
      await new Promise<void>((resolve) => {
        if (ctx.signal.aborted) return resolve();
        ctx.signal.addEventListener("abort", () => resolve());
      });
      return [[{ json: { cancelled: ctx.signal.aborted } }]];
    })
    .node({ type: "acme.test.noisy" }, () => {
      console.log("stray output");
      return [[{ json: {} }]];
    })
    .node({ type: "acme.test.wait" }, () => ({ outputs: [[{ json: {} }]], wait: { resumeToken: "tok" } }))
    .node({ type: "acme.test.cred" }, (ctx) => [[{ json: { key: ctx.credential("api").key } }]])
    .node({ type: "acme.test.big" }, () => [[{ json: { s: "x".repeat(5000) } }]]);
  return plugin;
}

export function executeParams(type: string, items: Item[] = [], params: object[] = [{}], extra: object = {}): Msg {
  return {
    callId: "c1",
    node: { type, version: 1 },
    inputs: [items],
    params,
    execution: { executionId: "ex_1", mode: "manual" },
    ...extra,
  };
}

/** Drives a plugin over in-memory streams. */
export class FakeHost {
  readonly toPlugin = new PassThrough();
  readonly fromPlugin = new PassThrough();
  readonly notifications: Msg[] = [];
  readonly responses = new Map<unknown, Msg>();
  readonly rawLines: string[] = [];
  private waiters: Array<() => void> = [];
  private nextId = 0;
  private buf = "";
  readonly served: Promise<void>;
  error: unknown;

  constructor(plugin: Plugin, private onRequest?: (m: Msg) => Promise<Msg> | Msg) {
    this.fromPlugin.on("data", (c: Buffer) => this.onData(c.toString("utf8")));
    this.served = plugin.serve({ input: this.toPlugin, output: this.fromPlugin }).catch((e) => {
      this.error = e;
    });
  }

  private onData(s: string): void {
    this.buf += s;
    let i: number;
    while ((i = this.buf.indexOf("\n")) >= 0) {
      const line = this.buf.slice(0, i);
      this.buf = this.buf.slice(i + 1);
      this.rawLines.push(line);
      const msg: Msg = JSON.parse(line);
      if (msg.method && msg.id !== undefined && this.onRequest) {
        void Promise.resolve(this.onRequest(msg)).then((r) => this.send({ jsonrpc: "2.0", id: msg.id, ...r }));
      } else if (msg.method) this.notifications.push(msg);
      else this.responses.set(msg.id, msg);
    }
    this.waiters.splice(0).forEach((w) => w());
  }

  send(msg: Msg): void {
    this.toPlugin.write(JSON.stringify(msg) + "\n");
  }

  start(method: string, params: unknown = {}): number {
    const id = ++this.nextId;
    this.send({ jsonrpc: "2.0", id, method, params });
    return id;
  }

  async until(cond: () => boolean, what: string, ms = 5000): Promise<void> {
    const deadline = Date.now() + ms;
    while (!cond()) {
      if (Date.now() > deadline) throw new Error(`timeout waiting for ${what}`);
      await new Promise<void>((r) => {
        this.waiters.push(r);
        setTimeout(r, 20);
      });
    }
  }

  async wait(id: unknown): Promise<Msg> {
    await this.until(() => this.responses.has(id), `response ${String(id)}`);
    return this.responses.get(id)!;
  }

  call(method: string, params: unknown = {}): Promise<Msg> {
    return this.wait(this.start(method, params));
  }

  async notification(method: string): Promise<Msg> {
    await this.until(() => this.notifications.some((n) => n.method === method), `${method} notification`);
    return this.notifications.find((n) => n.method === method)!;
  }

  async close(): Promise<void> {
    this.toPlugin.end();
    await this.served;
  }
}
