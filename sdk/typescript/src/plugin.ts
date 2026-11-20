import { Call, Context, type Transport } from "./context.js";
import {
  checkDescriptor, descriptorWire, normalizeDefinition,
} from "./descriptor.js";
import {
  CancelledError, Code, DescriptorError, NodeError, RpcError,
} from "./errors.js";
import type {
  Descriptor, ExecuteParams, Item, NodeDefinition, NodeOutput,
} from "./types.js";

export const PROTOCOL_VERSION = 1;
/** Default cap on one protocol message (bytes). Match the host's plugins.maxMessageBytes. */
export const DEFAULT_MAX_MESSAGE_BYTES = 32 << 20;
/** Default number of execute requests handled at once. Match plugins.maxConcurrentCalls. */
export const DEFAULT_MAX_CONCURRENT_CALLS = 16;

export type NodeHandler = (ctx: Context) => Promise<Item[][] | NodeOutput> | Item[][] | NodeOutput;

export interface PluginInfo {
  name: string;
  version: string;
  maxMessageBytes?: number;
  maxConcurrentCalls?: number;
}

export interface ServeOptions {
  /** Defaults to process.stdin. */
  input?: NodeJS.ReadableStream;
  /** Defaults to process.stdout. */
  output?: NodeJS.WritableStream;
}

export interface ServeHttpOptions {
  port: number;
  host?: string;
  /** If set, requests need `Authorization: Bearer <token>`. */
  token?: string;
}

/** An Engi plugin: register nodes with `node()` and call `serve()` or `serveHttp()`. */
export class Plugin {
  readonly name: string;
  readonly version: string;
  readonly maxMessageBytes: number;
  readonly maxConcurrentCalls: number;
  private readonly nodes = new Map<string, { desc: Descriptor; fn: NodeHandler }>();
  private active = 0;

  constructor(info: PluginInfo) {
    this.name = info.name;
    this.version = info.version;
    this.maxMessageBytes = info.maxMessageBytes ?? DEFAULT_MAX_MESSAGE_BYTES;
    this.maxConcurrentCalls = info.maxConcurrentCalls ?? DEFAULT_MAX_CONCURRENT_CALLS;
    if (this.maxMessageBytes < 1 || this.maxConcurrentCalls < 1) {
      throw new RangeError("maxMessageBytes and maxConcurrentCalls must be positive");
    }
  }

  /** Registers `fn` as node `def.type`. Ports default to one `main` input and output. */
  node(def: NodeDefinition, fn: NodeHandler): this {
    const desc = normalizeDefinition(def);
    const key = `${desc.type}@${desc.version}`;
    if (this.nodes.has(key)) throw new DescriptorError(`node ${desc.type} version ${desc.version} is registered twice`);
    this.nodes.set(key, { desc, fn });
    return this;
  }

  descriptors(): Descriptor[] {
    return [...this.nodes.values()].map((n) => n.desc);
  }

  /** Throws DescriptorError if any descriptor breaks a v1 rule. */
  validate(): void {
    const errs: string[] = [];
    if (this.nodes.size === 0) errs.push(`plugin "${this.name}" declares no nodes`);
    for (const d of this.descriptors()) {
      for (const e of checkDescriptor(d)) errs.push(`node "${d.type}" version ${d.version}: ${e}`);
    }
    if (errs.length) throw new DescriptorError(errs.join("; "));
  }

  /** Reserves a concurrency slot; false when at the cap. Pair with releaseSlot. */
  tryAcquireSlot(): boolean {
    if (this.active >= this.maxConcurrentCalls) return false;
    this.active++;
    return true;
  }

  releaseSlot(): void {
    this.active--;
  }

  /** Serves one request; throws RpcError. Shared by both transports. */
  async handle(method: string, params: unknown, transport: Transport, call: Call = new Call()): Promise<unknown> {
    switch (method) {
      case "initialize": {
        const v = (params as { protocolVersion?: unknown } | null)?.protocolVersion;
        if (typeof v !== "number" || v < PROTOCOL_VERSION) {
          throw new RpcError(Code.InvalidParams, `unsupported protocol version ${JSON.stringify(v)}; this plugin speaks ${PROTOCOL_VERSION}`);
        }
        return {
          protocolVersion: PROTOCOL_VERSION,
          plugin: { name: this.name, version: this.version },
          nodes: this.descriptors().map(descriptorWire),
        };
      }
      case "health":
        return {};
      case "execute":
        return this.execute(params, transport, call);
      default:
        throw new RpcError(Code.MethodNotFound, `method "${method}" not found`);
    }
  }

  private async execute(params: unknown, transport: Transport, call: Call): Promise<unknown> {
    const p = params as ExecuteParams | null;
    if (!p || typeof p !== "object" || !p.node || typeof p.node !== "object") {
      throw new RpcError(Code.InvalidParams, "execute params must be an object with a node");
    }
    const entry = this.nodes.get(`${p.node.type}@${p.node.version}`);
    if (!entry) throw new RpcError(Code.InvalidParams, `unknown node ${p.node.type} version ${p.node.version}`);
    call.callId = p.callId ?? "";
    const ctx = new Context(p, call, transport);
    let result: Item[][] | NodeOutput;
    try {
      result = await entry.fn(ctx);
    } catch (e) {
      if (e instanceof RpcError) throw e;
      if (e instanceof NodeError) throw e.toRpc();
      if (e instanceof CancelledError) throw new RpcError(Code.Cancelled, "cancelled");
      const err = e instanceof Error ? e : new Error(String(e));
      ctx.log.error(`node ${entry.desc.type} failed: ${err.stack ?? err.message}`);
      throw new RpcError(Code.InternalError, `${err.name}: ${err.message}`);
    }
    return normalizeOutput(entry.desc, result);
  }

  /**
   * Serves the stdio transport until shutdown or end of input. Resolves when done; rejects with
   * DescriptorError before serving, or MessageTooLargeError if the host sends an oversized message.
   * While serving, `console.log` and writes to `process.stdout` are routed to stderr.
   */
  async serve(opts: ServeOptions = {}): Promise<void> {
    const { runStdio } = await import("./stdio.js");
    this.validate();
    await runStdio(this, opts.input ?? process.stdin, opts.output ?? process.stdout);
  }

  /** Starts the HTTP transport (`POST /rpc`) and resolves with the listening server. */
  async serveHttp(opts: ServeHttpOptions): Promise<import("node:http").Server> {
    const { startHttp } = await import("./http.js");
    this.validate();
    return startHttp(this, opts);
  }
}

function normalizeOutput(desc: Descriptor, result: Item[][] | NodeOutput): unknown {
  let outputs: Item[][];
  let wait: NodeOutput["wait"];
  if (Array.isArray(result)) {
    outputs = result;
  } else if (result && typeof result === "object" && Array.isArray(result.outputs)) {
    ({ outputs, wait } = result);
  } else {
    throw new RpcError(Code.InternalError, "invalid node output: a node must return one array of items per output port");
  }
  let bad = "";
  if (!outputs.every(Array.isArray)) bad = "a node must return one array of items per output port";
  else if (outputs.length !== desc.outputs.length) {
    bad = `node ${desc.type} declares ${desc.outputs.length} output port(s) but returned ${outputs.length}`;
  } else if (!outputs.every((o) => o.every((i) => i && typeof i === "object" && i.json && typeof i.json === "object" && !Array.isArray(i.json)))) {
    bad = 'every item must be an object with a "json" object';
  }
  if (bad) throw new RpcError(Code.InternalError, `invalid node output: ${bad}`);
  const out: { outputs: Item[][]; wait?: Record<string, string> } = { outputs };
  if (wait) {
    const w: Record<string, string> = {};
    if (wait.until) w.until = wait.until instanceof Date ? wait.until.toISOString() : wait.until;
    if (wait.resumeToken) w.resumeToken = wait.resumeToken;
    if (Object.keys(w).length) out.wait = w;
  }
  return out;
}

/** Creates a plugin. */
export function definePlugin(info: PluginInfo): Plugin {
  return new Plugin(info);
}
