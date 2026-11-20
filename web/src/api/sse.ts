import { API_BASE } from '../config';
import type { StreamEvent } from './types';

export type StreamEventName =
  | 'execution.started' | 'node.started' | 'node.finished' | 'execution.waiting' | 'execution.finished';

const NAMES: StreamEventName[] = [
  'execution.started', 'node.started', 'node.finished', 'execution.waiting', 'execution.finished',
];

export interface Subscription {
  close(): void;
}

/** Subscribes to the live event stream of an execution. Same-origin cookies authenticate it. */
export function subscribeExecution(
  executionId: string,
  onEvent: (name: StreamEventName, event: StreamEvent) => void,
  onError?: () => void,
): Subscription {
  const es = new EventSource(`${API_BASE}/executions/${encodeURIComponent(executionId)}/stream`, { withCredentials: true });
  for (const name of NAMES) {
    es.addEventListener(name, (e) => {
      try {
        onEvent(name, JSON.parse((e as MessageEvent<string>).data) as StreamEvent);
      } catch {
        /* ignore a malformed frame */
      }
    });
  }
  es.onerror = () => {
    // The server closes the stream after the final event; EventSource would reconnect forever.
    es.close();
    onError?.();
  };
  return { close: () => es.close() };
}
