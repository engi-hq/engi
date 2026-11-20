import type { components } from './schema';

type S = components['schemas'];

export type User = S['User'];
export type Session = S['Session'];
export type Me = S['Me'];
export type EditorSettings = S['EditorSettings'];
export type WorkflowSummary = S['WorkflowSummary'];
export type ExecutionStatus = S['ExecutionStatus'];
export type ErrorInfo = S['ErrorInfo'];
export type CredentialSummary = S['CredentialSummary'];
export type Credential = S['Credential'];
export type TestResult = S['TestResult'];

export interface Page<T> {
  data: T[];
  nextCursor?: string;
  hasMore: boolean;
}

// ---- Descriptors (the schema leaves ports and options open, so they are refined here) ----

export type PropertyType =
  | 'string' | 'number' | 'boolean' | 'options' | 'multiOptions' | 'json' | 'code' | 'dateTime'
  | 'collection' | 'fixedCollection' | 'resourceLocator' | 'credentialsSelect' | 'notice';

export interface PropertyOption {
  name: string;
  value: string | number | boolean;
  description?: string;
}

export type DisplayRule = Record<string, Array<string | number | boolean>>;
export interface DisplayOptions {
  show?: DisplayRule;
  hide?: DisplayRule;
}

export interface Property {
  name: string;
  displayName?: string;
  type: PropertyType | (string & {});
  default?: unknown;
  required?: boolean;
  placeholder?: string;
  description?: string;
  options?: PropertyOption[];
  children?: Property[];
  displayOptions?: DisplayOptions;
  secret?: boolean;
}

export interface PortSpec {
  name: string;
  kind: string;
  displayName?: string;
  /** Sub-port inputs: the input must be connected. */
  required?: boolean;
  /** Sub-port inputs: most connections allowed; absent or 0 means unlimited. */
  maxConnections?: number;
}

export interface CredentialSlot {
  name: string;
  type: string;
  required?: boolean;
}

export interface NodeType {
  type: string;
  version: number;
  displayName: string;
  description?: string;
  group: string;
  icon?: string;
  inputs: PortSpec[];
  outputs: PortSpec[];
  properties: Property[];
  credentials: CredentialSlot[];
  mergePolicy?: string;
}

export interface CredentialType {
  name: string;
  displayName: string;
  icon?: string;
  fields: Property[];
  testable: boolean;
  oauth2: boolean;
  authenticates: boolean;
}

// ---- Workflow document (docs/03) ----

export interface NodeSettings {
  retry?: { maxAttempts?: number; backoff?: string; initialDelayMs?: number };
  onError?: 'stop' | 'continue' | 'errorOutput';
  alwaysOutputData?: boolean;
  executeOnce?: boolean;
  timeoutMs?: number;
}

export interface WfNode {
  id?: string;
  name: string;
  type: string;
  typeVersion?: number;
  position?: [number, number];
  parameters?: Record<string, unknown>;
  credentials?: Record<string, string>;
  disabled?: boolean;
  settings?: NodeSettings;
  notes?: string;
}

export interface Endpoint {
  node: string;
  port: string;
  index: number;
}
export interface Connection {
  from: Endpoint;
  to: Endpoint;
}

export interface WorkflowDoc {
  id?: string;
  name: string;
  active?: boolean;
  version?: number;
  settings?: Record<string, unknown>;
  nodes: WfNode[];
  connections: Connection[];
  pinData?: Record<string, unknown>;
  tags?: string[];
  notes?: WfNote[];
}

/** A sticky note on the canvas (docs/03 section 3.9). The engine never sees it. */
export interface WfNote {
  id: string;
  /** Markdown source. */
  text?: string;
  position: [number, number];
  size: { width: number; height: number };
  /** Palette name; omitted means yellow. */
  color?: string;
}

// ---- Executions ----

export interface Item {
  json: Record<string, unknown>;
  binary?: Record<string, unknown>;
  error?: unknown;
}

export interface NodeRun {
  runIndex: number;
  startedAt?: string;
  durationMs?: number;
  status: string;
  attempts?: number;
  input?: { main?: Item[][] };
  output?: { main?: Item[][] };
  error?: ErrorInfo;
}

export interface Execution {
  id: string;
  workflowId: string;
  workflowVersion: number;
  mode: string;
  status: ExecutionStatus;
  startedAt: string;
  finishedAt?: string;
  waitUntil?: string;
  /** How many times a worker started the run; 1 for a run that went through once. */
  attempts?: number;
  /** When a dead worker last lost the run and a reclaim took it back. */
  lastRecoveredAt?: string;
  error?: ErrorInfo;
  runData?: Record<string, NodeRun[]>;
}

export interface StreamEvent {
  executionId: string;
  workflowId?: string;
  at: string;
  node?: string;
  runIndex?: number;
  status?: ExecutionStatus;
  nodeStatus?: string;
  durationMs?: number;
  outputItems?: number[];
  error?: ErrorInfo;
}

export interface ArmedWebhook {
  method: string;
  urlPath: string;
  expiresAt: string;
}
