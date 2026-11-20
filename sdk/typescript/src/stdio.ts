import { Call, type Transport } from "./context.js";
import {
  CancelledError, Code, HostError, MessageTooLargeError, RpcError, UnsupportedError,
} from "./errors.js";
import type { Plugin } from "./plugin.js";
import type { Logger, LogLevel } from "./types.js";

type Id = string | number | null;
type Message = {
  id?: Id;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code?: number; message?: string; data?: { description?: string; httpStatus?: number; itemIndex?: number } };
};

const NEWLINE = 0x0a;

/** Serves the stdio transport. See Plugin.serve. */
export async function runStdio(
  plugin: Plugin,
  input: NodeJS.ReadableStream,
  output: NodeJS.WritableStream,
): Promise<void> {
  const realWrite = output.write.bind(output) as (chunk: Buffer, cb?: (err?: Error | null) => void) => boolean;
  const restore = redirectStdout(output);
  try {
    await new StdioSession(plugin, input, realWrite).run();
  } finally {
    restore();
  }
}

/** Routes console.log/info/debug (and process.stdout.write if it is the protocol stream) to stderr. */
function redirectStdout(output: NodeJS.WritableStream): () => void {
  const saved = { log: console.log, info: console.info, debug: console.debug };
  const toStderr = console.error.bind(console);
  console.log = toStderr;
  console.info = toStderr;
  console.debug = toStderr;
  const savedWrite = process.stdout.write;
  const patchStdout = output === process.stdout;
  if (patchStdout) {
    process.stdout.write = ((chunk: unknown, ...rest: unknown[]) =>
      (process.stderr.write as (...a: unknown[]) => boolean)(chunk, ...rest)) as typeof process.stdout.write;
  }
  return () => {
    console.log = saved.log;
    console.info = saved.info;
    console.debug = saved.debug;
    if (patchStdout) process.stdout.write = savedWrite;
  };
}

interface Pending {
  resolve: (v: Record<string, unknown>) => void;
  reject: (e: Error) => void;
}

class StdioSession implements Transport {
  readonly logger: Logger;
  private readonly max: number;
  private buf: Buffer = Buffer.alloc(0);
  private nextId = 0;
  private readonly pending = new Map<number, Pending>();
  private readonly calls = new Map<string, Call>();
  private readonly running = new Set<Promise<void>>();
  private shuttingDown = false;
  private closed = false;
  private fatal: Error | undefined;
  private finish!: () => void;
  private readonly done: Promise<void>;

  constructor(
    private readonly plugin: Plugin,
    private readonly input: NodeJS.ReadableStream,
    private readonly write: (chunk: Buffer, cb?: (err?: Error | null) => void) => boolean,
  ) {
    this.max = plugin.maxMessageBytes;
    this.done = new Promise((resolve) => (this.finish = resolve));
    this.logger = this.loggerFor("");
  }

  loggerFor(callId: string): Logger {
    const level = (l: LogLevel) => (message: string, attrs?: Record<string, unknown>) => {
      const params: Record<string, unknown> = { level: l, message };
      if (attrs && Object.keys(attrs).length) params.attrs = attrs;
      if (callId) params.callId = callId;
      this.send({ method: "log", params });
    };
    return { debug: level("debug"), info: level("info"), warn: level("warn"), error: level("error") };
  }

  async run(): Promise<void> {
    const onData = (chunk: Buffer | string) => this.onData(typeof chunk === "string" ? Buffer.from(chunk) : chunk);
    const onEnd = () => {
      if (this.buf.length) {
        const rest = this.buf;
        this.buf = Buffer.alloc(0);
        this.onLine(rest);
      }
      this.finish();
    };
    this.input.on("data", onData);
    this.input.on("end", onEnd);
    this.input.on("close", this.finish);
    this.input.on("error", this.finish);
    await this.done;
    this.input.off("data", onData);
    this.input.off("end", onEnd);
    this.closed = true;
    this.cancelAll();
    while (this.running.size) await Promise.allSettled([...this.running]);
    await new Promise<void>((resolve) => this.write(Buffer.alloc(0), () => resolve()));
    (this.input as { destroy?: () => void }).destroy?.();
    if (this.fatal) throw this.fatal;
  }

  private onData(chunk: Buffer): void {
    if (this.fatal) return;
    this.buf = this.buf.length ? Buffer.concat([this.buf, chunk]) : chunk;
    for (;;) {
      const idx = this.buf.indexOf(NEWLINE);
      if (idx < 0) break;
      if (idx + 1 > this.max) return this.fail();
      const line = this.buf.subarray(0, idx);
      this.buf = this.buf.subarray(idx + 1);
      this.onLine(line);
    }
    if (this.buf.length > this.max) this.fail();
  }

  private fail(): void {
    this.fatal = new MessageTooLargeError(`message exceeds the limit of ${this.max} bytes`);
    this.finish();
  }

  private onLine(line: Buffer): void {
    if (!line.toString("utf8").trim()) return;
    let msg: Message;
    try {
      msg = JSON.parse(line.toString("utf8"));
    } catch {
      return this.replyError(null, new RpcError(Code.ParseError, "invalid JSON"));
    }
    if (!msg || typeof msg !== "object" || Array.isArray(msg)) {
      return this.replyError(null, new RpcError(Code.InvalidRequest, "message must be an object"));
    }
    this.dispatch(msg);
  }

  private dispatch(msg: Message): void {
    const { id, method, params } = msg;
    if (method === undefined) {
      if (typeof id === "number") this.resolve(id, msg);
      return;
    }
    if (method === "$/cancel") {
      const target = (params as { id?: Id } | undefined)?.id;
      this.calls.get(JSON.stringify(target))?.abort.abort();
      this.rejectPending(this.calls.get(JSON.stringify(target)));
      return;
    }
    if (method === "shutdown") {
      this.shuttingDown = true;
      if (id !== undefined && id !== null) this.reply(id, {});
      if (this.running.size === 0) this.finish();
      return;
    }
    if (id === undefined || id === null) return; // unknown notification
    if (method === "execute") return this.startExecute(id, params);
    this.plugin.handle(method, params, this).then(
      (r) => this.reply(id, r),
      (e) => this.replyError(id, toRpc(e)),
    );
  }

  private startExecute(id: Id, params: unknown): void {
    if (this.shuttingDown) return this.replyError(id, new RpcError(Code.InvalidRequest, "plugin is shutting down"));
    if (!this.plugin.tryAcquireSlot()) return this.replyError(id, new RpcError(Code.Busy, "too many concurrent requests"));
    const call = new Call();
    const key = JSON.stringify(id);
    this.calls.set(key, call);
    const task: Promise<void> = this.plugin
      .handle("execute", params, this, call)
      .then(
        (r) => this.reply(id, r),
        (e) => this.replyError(id, toRpc(e)),
      )
      .finally(() => {
        this.plugin.releaseSlot();
        this.calls.delete(key);
        this.running.delete(task);
        if (this.shuttingDown && this.running.size === 0) this.finish();
      });
    this.running.add(task);
  }

  private resolve(id: number, msg: Message): void {
    const p = this.pending.get(id);
    if (!p) return;
    this.pending.delete(id);
    if (msg.error) return p.reject(hostError(msg.error));
    p.resolve(msg.result && typeof msg.result === "object" ? (msg.result as Record<string, unknown>) : {});
  }

  private rejectPending(call: Call | undefined): void {
    if (!call) return;
    for (const pid of call.pending) {
      this.pending.get(pid)?.reject(new CancelledError());
      this.pending.delete(pid);
    }
    call.pending.clear();
  }

  private cancelAll(): void {
    for (const call of this.calls.values()) {
      call.abort.abort();
      this.rejectPending(call);
    }
  }

  request(call: Call, method: string, params: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (call.abort.signal.aborted) return Promise.reject(new CancelledError());
    if (this.closed) return Promise.reject(new HostError("host connection closed"));
    const pid = ++this.nextId;
    return new Promise((resolve, reject) => {
      call.pending.add(pid);
      this.pending.set(pid, {
        resolve: (v) => { call.pending.delete(pid); resolve(v); },
        reject: (e) => { call.pending.delete(pid); reject(e); },
      });
      try {
        this.send({ id: pid, method, params });
      } catch (e) {
        this.pending.delete(pid);
        call.pending.delete(pid);
        reject(new HostError(`sending ${method}: ${(e as Error).message}`));
      }
    });
  }

  private send(msg: Record<string, unknown>): void {
    const data = Buffer.from(JSON.stringify({ jsonrpc: "2.0", ...msg }) + "\n", "utf8");
    if (data.length > this.max) {
      throw new MessageTooLargeError(`sending ${data.length} bytes exceeds the limit of ${this.max}`);
    }
    this.write(data);
  }

  private reply(id: Id, result: unknown): void {
    try {
      this.send({ id, result });
    } catch (e) {
      this.replyError(id, new RpcError(Code.InternalError, `encoding result: ${(e as Error).message}`));
    }
  }

  private replyError(id: Id, err: RpcError): void {
    try {
      this.send({ id, error: err.toWire() });
    } catch {
      this.send({ id, error: new RpcError(Code.InternalError, err.message.slice(0, 200)).toWire() });
    }
  }
}

function toRpc(e: unknown): RpcError {
  if (e instanceof RpcError) return e;
  const err = e instanceof Error ? e : new Error(String(e));
  return new RpcError(Code.InternalError, `${err.name}: ${err.message}`);
}

function hostError(err: NonNullable<Message["error"]>): HostError {
  const code = err.code ?? Code.InternalError;
  const message = err.message ?? "host error";
  if (code === Code.Unsupported) {
    const e = new UnsupportedError("callback");
    e.message = message;
    return e;
  }
  const d = code === Code.NodeError ? err.data ?? {} : {};
  return new HostError(message, code, { description: d.description, httpStatus: d.httpStatus, itemIndex: d.itemIndex });
}
