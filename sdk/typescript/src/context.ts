import { CancelledError, CredentialError, ParamError } from "./errors.js";
import type {
  BinaryRef, ExecuteParams, ExecutionInfo, Item, Logger,
} from "./types.js";

/** State of one execute request: cancellation and its in-flight callbacks. */
export class Call {
  readonly abort = new AbortController();
  readonly pending = new Set<number>();
  callId = "";
}

/** How a Context reaches the host; implemented per transport. */
export interface Transport {
  /** Logger for lines outside any call (sent without a callId). */
  readonly logger: Logger;
  /** Logger for one call: its lines carry the call's callId so the host logs them with that run. */
  loggerFor(callId: string): Logger;
  request(call: Call, method: string, params: Record<string, unknown>): Promise<Record<string, unknown>>;
}

export interface Blob {
  /** Returns the bytes of the blob that `ref` names. */
  read(ref: BinaryRef): Promise<Uint8Array>;
  /** Stores `data` as a blob and returns its BinaryRef. */
  write(name: string, mime: string, data: Uint8Array): Promise<BinaryRef>;
}

/** Everything a node needs for one execute call. */
export class Context {
  readonly nodeType: string;
  readonly nodeVersion: number;
  readonly callId: string;
  /** Execution info (copy). */
  readonly execution: ExecutionInfo;
  /** Aborted when the host sends `$/cancel`. */
  readonly signal: AbortSignal;
  readonly log: Logger;
  readonly blob: Blob;

  constructor(
    private readonly p: ExecuteParams,
    private readonly call: Call,
    private readonly transport: Transport,
  ) {
    this.nodeType = p.node.type;
    this.nodeVersion = p.node.version;
    this.callId = call.callId;
    this.execution = structuredClone(p.execution ?? {});
    this.signal = call.abort.signal;
    this.log = transport.loggerFor(call.callId);
    this.blob = {
      read: async (ref) => {
        const res = await this.request("blob.read", { ref });
        return new Uint8Array(Buffer.from(String(res.data ?? ""), "base64"));
      },
      write: async (name, mime, data) => {
        if (!(data instanceof Uint8Array)) throw new TypeError("blob.write data must be a Uint8Array");
        const res = await this.request("blob.write", { name, mime, data: Buffer.from(data).toString("base64") });
        return res as unknown as BinaryRef;
      },
    };
  }

  /** Returns a deep copy of the items on main input `port` (empty if absent). */
  items(port = 0): Item[] {
    return structuredClone(this.p.inputs?.[port] ?? []);
  }

  /**
   * Returns property `name` resolved for item `itemIndex` of input 0. Throws ParamError with the
   * host's message if it failed to resolve; an absent property is `undefined`.
   */
  param<T = unknown>(name: string, itemIndex = 0): T | undefined {
    const sets = this.p.params?.length ? this.p.params : [{}];
    if (itemIndex < 0 || itemIndex >= sets.length) {
      throw new RangeError(`itemIndex ${itemIndex} out of range (${sets.length} parameter sets)`);
    }
    const msg = this.p.paramErrors?.[itemIndex]?.[name];
    if (msg !== undefined) throw new ParamError(msg, { itemIndex });
    const set = sets[itemIndex];
    return name in set ? (structuredClone(set[name]) as T) : undefined;
  }

  /** Returns the decrypted data of credential `name`. Never log it. */
  credential(name: string): Record<string, unknown> {
    const err = this.p.credentialErrors?.[name];
    if (err !== undefined) throw new CredentialError(err);
    const cred = this.p.credentials?.[name];
    if (!cred) throw new CredentialError(`credential "${name}" is not bound to this node`);
    return structuredClone(cred.data ?? {});
  }

  /** Evaluates an expression in the scope of item `itemIndex` (stdio only). */
  async eval(expression: string, itemIndex = 0): Promise<unknown> {
    const res = await this.request("expression.eval", { expression, itemIndex });
    return res.value;
  }

  private request(method: string, params: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (this.signal.aborted) return Promise.reject(new CancelledError());
    return this.transport.request(this.call, method, { ...params, callId: this.callId });
  }
}
