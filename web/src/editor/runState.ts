import type { Execution, NodeRun, StreamEvent } from '../api/types';

export type NodeStatus = 'running' | 'success' | 'error' | 'waiting' | 'canceled';

export interface NodeRunState {
  status: NodeStatus;
  /** Items per output port, from the last finished run. */
  outputItems?: number[];
  durationMs?: number;
  error?: string;
}

export type RunStates = Record<string, NodeRunState>;

const countItems = (run: NodeRun): number[] | undefined => run.output?.main?.map((port) => port?.length ?? 0);

const toStatus = (s: string): NodeStatus =>
  s === 'success' ? 'success' : s === 'error' ? 'error' : s === 'running' ? 'running' : s === 'waiting' ? 'waiting' : 'canceled';

/** Canvas state of a finished (or stored) execution: the last run of each node. */
export function statesFromExecution(ex: Execution | undefined): RunStates {
  const out: RunStates = {};
  if (!ex?.runData) return out;
  for (const [name, runs] of Object.entries(ex.runData)) {
    const last = runs[runs.length - 1];
    if (!last) continue;
    out[name] = { status: toStatus(last.status), outputItems: countItems(last), durationMs: last.durationMs, error: last.error?.message };
  }
  return out;
}

/** Folds one SSE event into the node states. */
export function applyStreamEvent(states: RunStates, name: string, e: StreamEvent): RunStates {
  if (!e.node) return states;
  if (name === 'node.started') return { ...states, [e.node]: { status: 'running' } };
  if (name === 'node.finished') {
    const status = toStatus(e.nodeStatus ?? 'success');
    return { ...states, [e.node]: { status, outputItems: e.outputItems, durationMs: e.durationMs, error: e.error?.message } };
  }
  return states;
}

/** Pick the item to preview expressions against: the first input item of the node's last run. */
export function lastRun(ex: Execution | undefined, node: string): NodeRun | undefined {
  const runs = ex?.runData?.[node];
  return runs?.[runs.length - 1];
}
