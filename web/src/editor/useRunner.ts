import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import { fetchExecution, runWorkflow, type RunBody } from '../api/hooks';
import { subscribeExecution, type Subscription } from '../api/sse';
import type { Execution } from '../api/types';
import { applyStreamEvent, statesFromExecution, type RunStates } from './runState';

export type RunPhase = 'idle' | 'starting' | 'running' | 'finished';

export interface RunnerState {
  phase: RunPhase;
  executionId?: string;
  states: RunStates;
  execution?: Execution;
  error?: string;
}

const FINAL = new Set(['success', 'error', 'canceled', 'crashed']);
const POLL_MS = 1000;

/** Starts or attaches to an execution and follows it over SSE, falling back to polling. */
export function useRunner(workflowId: string | undefined, onFinished?: (ex: Execution) => void) {
  const [state, setState] = useState<RunnerState>({ phase: 'idle', states: {} });
  const sub = useRef<Subscription | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const token = useRef(0);
  const finished = useRef(onFinished);
  finished.current = onFinished;

  const stop = useCallback(() => {
    sub.current?.close();
    sub.current = null;
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  }, []);
  useEffect(() => stop, [stop]);

  const finish = useCallback(async (id: string, my: number) => {
    try {
      const ex = await fetchExecution(id);
      if (my !== token.current) return;
      if (!FINAL.has(ex.status)) {
        timer.current = setTimeout(() => void finish(id, my), POLL_MS);
        return;
      }
      stop();
      setState({ phase: 'finished', executionId: id, states: statesFromExecution(ex), execution: ex });
      finished.current?.(ex);
    } catch (e) {
      if (my === token.current) setState((s) => ({ ...s, phase: 'finished', error: e instanceof Error ? e.message : String(e) }));
    }
  }, [stop]);

  const attach = useCallback((executionId: string) => {
    stop();
    const my = ++token.current;
    setState({ phase: 'running', executionId, states: {} });
    sub.current = subscribeExecution(
      executionId,
      (name, ev) => {
        if (my !== token.current) return;
        if (name === 'execution.finished') void finish(executionId, my);
        else if (name === 'execution.waiting') setState((s) => ({ ...s, phase: 'running' }));
        else setState((s) => ({ ...s, states: applyStreamEvent(s.states, name, ev) }));
      },
      // The stream closed without a final event (proxy, restart): poll instead.
      () => { if (my === token.current && !timer.current) timer.current = setTimeout(() => void finish(executionId, my), POLL_MS); },
    );
  }, [stop, finish]);

  const run = useCallback(async (body: RunBody = {}) => {
    if (!workflowId) return;
    stop();
    setState({ phase: 'starting', states: {} });
    try {
      const { executionId } = await runWorkflow(workflowId, body);
      attach(executionId);
    } catch (e) {
      const msg = e instanceof ApiError ? [e.message, ...(e.problem.errors?.map((x) => `${x.pointer}: ${x.message}`) ?? [])].join(' ') : String(e);
      setState({ phase: 'idle', states: {}, error: msg });
    }
  }, [workflowId, stop, attach]);

  /** Shows a stored execution without following it. */
  const show = useCallback((ex: Execution) => {
    stop();
    token.current++;
    setState({ phase: 'finished', executionId: ex.id, states: statesFromExecution(ex), execution: ex });
  }, [stop]);

  const reset = useCallback(() => {
    stop();
    token.current++;
    setState({ phase: 'idle', states: {} });
  }, [stop]);

  return { state, run, attach, show, reset };
}
