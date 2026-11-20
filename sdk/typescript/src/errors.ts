/** JSON-RPC error codes of the Engi Plugin Protocol v1 (docs/15 section 15.3). */
export const Code = {
  ParseError: -32700,
  InvalidRequest: -32600,
  MethodNotFound: -32601,
  InvalidParams: -32602,
  InternalError: -32603,
  /** A failure shown to the user like a built-in node's error. */
  NodeError: -32001,
  /** Answers a request that was cancelled. */
  Cancelled: -32002,
  /** Answers a method the transport does not offer, such as a callback over HTTP. */
  Unsupported: -32003,
  /** Answers a request above the plugin's concurrency cap. */
  Busy: -32004,
} as const;

/** A JSON-RPC error object, thrown or received. */
export class RpcError extends Error {
  constructor(
    public readonly code: number,
    message: string,
    public readonly data?: unknown,
  ) {
    super(message);
    this.name = "RpcError";
  }

  toWire(): { code: number; message: string; data?: unknown } {
    return this.data === undefined
      ? { code: this.code, message: this.message }
      : { code: this.code, message: this.message, data: this.data };
  }
}

export interface NodeErrorOptions {
  description?: string;
  httpStatus?: number;
  itemIndex?: number;
}

/** Fails the node with a message shown to the user (code -32001). */
export class NodeError extends Error {
  readonly description?: string;
  readonly httpStatus?: number;
  readonly itemIndex?: number;

  constructor(message: string, options: NodeErrorOptions = {}) {
    super(message);
    this.name = "NodeError";
    this.description = options.description;
    this.httpStatus = options.httpStatus;
    this.itemIndex = options.itemIndex;
  }

  toRpc(): RpcError {
    const data: NodeErrorOptions = {};
    if (this.description) data.description = this.description;
    if (this.httpStatus) data.httpStatus = this.httpStatus;
    if (this.itemIndex !== undefined) data.itemIndex = this.itemIndex;
    return new RpcError(Code.NodeError, this.message, Object.keys(data).length ? data : undefined);
  }
}

/** A parameter the host could not resolve; the message is the host's. */
export class ParamError extends NodeError {
  constructor(message: string, options: NodeErrorOptions = {}) {
    super(message, options);
    this.name = "ParamError";
  }
}

/** A credential that is not declared, not bound, or could not be loaded. */
export class CredentialError extends NodeError {
  constructor(message: string) {
    super(message);
    this.name = "CredentialError";
  }
}

/** The host answered a callback with an error. Uncaught, it fails the node. */
export class HostError extends NodeError {
  constructor(
    message: string,
    public readonly code: number = Code.InternalError,
    options: NodeErrorOptions = {},
  ) {
    super(message, options);
    this.name = "HostError";
  }
}

/** The callback is not available over this transport (code -32003). */
export class UnsupportedError extends HostError {
  constructor(what: string) {
    super(`${what} is not supported over this transport`, Code.Unsupported);
    this.name = "UnsupportedError";
  }

  override toRpc(): RpcError {
    return new RpcError(Code.Unsupported, this.message);
  }
}

/** The host cancelled the call (`$/cancel`); answered with code -32002. */
export class CancelledError extends Error {
  constructor(message = "call cancelled") {
    super(message);
    this.name = "CancelledError";
  }
}

/** A node descriptor breaks a v1 rule (docs/15 section 15.4). */
export class DescriptorError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DescriptorError";
  }
}

/** A message exceeded the configured size limit; the connection is closed. */
export class MessageTooLargeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MessageTooLargeError";
  }
}
