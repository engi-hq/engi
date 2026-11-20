/** Wire shapes of the Engi Plugin Protocol v1 (mirrors pkg/pluginproto). */

export interface BinaryRef {
  ref: string;
  mime?: string;
  name?: string;
  size?: number;
}

export interface PairedItem {
  input: number;
  item: number;
}

export interface ItemError {
  node?: string;
  message: string;
  description?: string;
  httpStatus?: number;
}

export interface Item {
  json: Record<string, unknown>;
  binary?: Record<string, BinaryRef>;
  pairedItem?: PairedItem[];
  error?: ItemError;
}

export type PropertyType =
  | "string" | "number" | "boolean" | "options" | "multiOptions" | "json" | "code" | "dateTime"
  | "collection" | "fixedCollection" | "resourceLocator" | "credentialsSelect" | "notice";

export interface Option {
  name: string;
  value: unknown;
  description?: string;
}

export interface DisplayOptions {
  show?: Record<string, unknown[]>;
  hide?: Record<string, unknown[]>;
}

export interface Property {
  name: string;
  type: PropertyType;
  displayName?: string;
  /** `undefined` and `null` mean "no default". */
  default?: unknown;
  required?: boolean;
  placeholder?: string;
  description?: string;
  options?: Option[];
  children?: Property[];
  displayOptions?: DisplayOptions;
}

export interface PortSpec {
  name: string;
  /** v1 plugins may only use "main" (the default). */
  kind?: string;
  displayName?: string;
  required?: boolean;
  maxConnections?: number;
}

export interface CredentialRef {
  name: string;
  type: string;
  required?: boolean;
}

export type Group = "action" | "transform" | "flow";
export type MergePolicy = "" | "waitAll" | "any" | "partialOnFlush";

/** What a plugin declares for one node type. */
export interface NodeDefinition {
  type: string;
  version?: number;
  displayName?: string;
  group?: Group;
  description?: string;
  icon?: string;
  properties?: Property[];
  /** Defaults to one `main` port; pass `[]` for none. Strings are port names. */
  inputs?: Array<PortSpec | string>;
  outputs?: Array<PortSpec | string>;
  credentials?: CredentialRef[];
  mergePolicy?: MergePolicy;
}

/** A normalised node descriptor (the wire form is derived from it). */
export interface Descriptor {
  type: string;
  version: number;
  displayName: string;
  group: string;
  description: string;
  icon: string;
  properties: Property[];
  inputs: PortSpec[];
  outputs: PortSpec[];
  credentials: CredentialRef[];
  mergePolicy: string;
}

export interface Credential {
  type: string;
  id: string;
  version: number;
  data: Record<string, unknown>;
}

export interface ExecutionInfo {
  executionId?: string;
  workflowId?: string;
  workflowName?: string;
  mode?: string;
  nodeName?: string;
  runIndex?: number;
  attempt?: number;
  depth?: number;
}

export interface ExecuteParams {
  callId: string;
  node: { type: string; version: number };
  inputs: Item[][];
  params: Array<Record<string, unknown>>;
  paramErrors?: Array<Record<string, string>>;
  credentials?: Record<string, Credential>;
  credentialErrors?: Record<string, string>;
  execution: ExecutionInfo;
}

export interface Wait {
  until?: Date | string;
  resumeToken?: string;
}

/** A node result with a wait request; a plain `Item[][]` needs no wrapper. */
export interface NodeOutput {
  outputs: Item[][];
  wait?: Wait;
}

export type LogLevel = "debug" | "info" | "warn" | "error";
export type LogFn = (message: string, attrs?: Record<string, unknown>) => void;

export interface Logger {
  debug: LogFn;
  info: LogFn;
  warn: LogFn;
  error: LogFn;
}
