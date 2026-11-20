import http from "node:http";
import { Call, type Transport } from "./context.js";
import { Code, RpcError, UnsupportedError } from "./errors.js";
import type { Plugin, ServeHttpOptions } from "./plugin.js";
import type { Logger } from "./types.js";
import { timingSafeEqual } from "node:crypto";

function stderrLogger(): Logger {
  const at = (level: string) => (message: string, attrs?: Record<string, unknown>) =>
    console.error(`${level.toUpperCase()} ${message}${attrs ? " " + JSON.stringify(attrs) : ""}`);
  return { debug: at("debug"), info: at("info"), warn: at("warn"), error: at("error") };
}

class HttpTransport implements Transport {
  readonly logger = stderrLogger();
  /** HTTP has no log callback: every call logs to stderr. */
  loggerFor(): Logger {
    return this.logger;
  }
  request(_call: Call, method: string): Promise<Record<string, unknown>> {
    return Promise.reject(new UnsupportedError(method));
  }
}

function sameToken(given: string, token: string): boolean {
  const a = Buffer.from(given);
  const b = Buffer.from(`Bearer ${token}`);
  return a.length === b.length && timingSafeEqual(a, b);
}

/** Starts `POST /rpc`; resolves with the listening server. Callbacks are unsupported. */
export function startHttp(plugin: Plugin, opts: ServeHttpOptions): Promise<http.Server> {
  const transport = new HttpTransport();
  const max = plugin.maxMessageBytes;

  const send = (res: http.ServerResponse, status: number, body?: unknown): void => {
    if (body === undefined) {
      res.writeHead(status, { "Content-Length": 0 }).end();
      return;
    }
    const data = Buffer.from(JSON.stringify(body));
    res.writeHead(status, { "Content-Type": "application/json", "Content-Length": data.length }).end(data);
  };

  const answer = async (body: Buffer): Promise<{ status: number; body?: unknown }> => {
    let msg: { id?: string | number | null; method?: unknown; params?: unknown };
    try {
      msg = JSON.parse(body.toString("utf8"));
    } catch {
      return { status: 200, body: { jsonrpc: "2.0", id: null, error: new RpcError(Code.ParseError, "invalid JSON").toWire() } };
    }
    if (!msg || typeof msg !== "object" || typeof msg.method !== "string") {
      return { status: 200, body: { jsonrpc: "2.0", id: null, error: new RpcError(Code.InvalidRequest, "not a JSON-RPC request").toWire() } };
    }
    if (msg.id === undefined || msg.id === null) return { status: 204 }; // $/cancel, shutdown: no meaning for a service
    const resp: Record<string, unknown> = { jsonrpc: "2.0", id: msg.id };
    const slot = msg.method === "execute";
    if (slot && !plugin.tryAcquireSlot()) {
      resp.error = new RpcError(Code.Busy, "too many concurrent requests").toWire();
      return { status: 200, body: resp };
    }
    try {
      resp.result = await plugin.handle(msg.method, msg.params, transport);
    } catch (e) {
      const err = e instanceof RpcError ? e : new RpcError(Code.InternalError, e instanceof Error ? `${e.name}: ${e.message}` : String(e));
      resp.error = err.toWire();
    } finally {
      if (slot) plugin.releaseSlot();
    }
    return { status: 200, body: resp };
  };

  const server = http.createServer((req, res) => {
    if (req.url !== "/rpc") return send(res, 404);
    if (req.method !== "POST") return send(res, 405);
    if (opts.token !== undefined && !sameToken(req.headers.authorization ?? "", opts.token)) return send(res, 401);
    const len = Number(req.headers["content-length"]);
    if (!Number.isFinite(len)) return send(res, 411);
    if (len > max) return send(res, 413);
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      answer(Buffer.concat(chunks)).then(({ status, body }) => {
        if (body !== undefined && Buffer.byteLength(JSON.stringify(body)) > max) {
          const id = (body as { id?: unknown }).id;
          return send(res, status, { jsonrpc: "2.0", id, error: new RpcError(Code.InternalError, "response exceeds the message limit").toWire() });
        }
        send(res, status, body);
      });
    });
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(opts.port, opts.host, () => {
      server.off("error", reject);
      resolve(server);
    });
  });
}
