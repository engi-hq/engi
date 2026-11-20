import { useEffect, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import { previewExpressionRemote } from '../api/hooks';
import type { WorkflowDoc } from '../api/types';
import { useDebounced } from '../util/useDebounced';

/** Where the server should evaluate an expression: the node and the past execution to take input from. */
export interface ServerPreviewTarget {
  node: string;
  workflowId?: string;
  executionId?: string;
  /** The unsaved document, so the evaluator sees the current node names. */
  workflow?: WorkflowDoc;
}

export type RemotePreview =
  | { state: 'idle' }
  | { state: 'loading' }
  | { state: 'value'; value: string }
  | { state: 'error'; message: string }
  /** The server could not evaluate (no past execution, offline): callers fall back to the local preview. */
  | { state: 'unavailable' };

export const PREVIEW_DEBOUNCE_MS = 400;

const show = (v: unknown): string => (typeof v === 'string' ? v : JSON.stringify(v) ?? 'undefined');

/** Debounced server-side expression preview; in-flight requests are aborted when the input changes. */
export function useExpressionPreview(expression: string, target: ServerPreviewTarget | undefined, ms = PREVIEW_DEBOUNCE_MS): RemotePreview {
  const debounced = useDebounced(expression, ms);
  const [result, setResult] = useState<RemotePreview>({ state: 'idle' });
  const enabled = !!target && !!(target.executionId || target.workflowId);
  const { node, workflowId, executionId, workflow } = target ?? { node: '' };
  // Read through a ref: the document changes on every keystroke and must not retrigger the debounced request.
  const wf = useRef(workflow);
  wf.current = workflow;

  useEffect(() => {
    if (!enabled || debounced.slice(1).trim() === '') { setResult({ state: 'idle' }); return; }
    const ctl = new AbortController();
    setResult({ state: 'loading' });
    previewExpressionRemote(
      { expression: debounced, node, itemIndex: 0, runIndex: 0, ...(executionId ? { executionId } : { workflowId }), workflow: wf.current },
      ctl.signal,
    )
      .then((r) => { if (!ctl.signal.aborted) setResult({ state: 'value', value: show(r.value) }); })
      .catch((e: unknown) => {
        if (ctl.signal.aborted || (e instanceof DOMException && e.name === 'AbortError')) return;
        if (e instanceof ApiError && e.status === 422 && e.problem.type?.endsWith('expression-error')) {
          setResult({ state: 'error', message: e.problem.detail ?? e.problem.title });
        } else setResult({ state: 'unavailable' });
      });
    return () => ctl.abort();
  }, [enabled, debounced, node, workflowId, executionId]);

  // Between a keystroke and the debounced request the shown result is stale.
  return enabled && debounced !== expression && result.state !== 'idle' ? { state: 'loading' } : result;
}
