import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useExecutionMutations, useExecutions, useWorkflows } from '../api/hooks';
import type { ExecutionStatus } from '../api/types';
import { EXECUTIONS_REFRESH_MS } from '../config';
import { t } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { describeError, useToast } from '../ui/Toast';

export const ALL_STATUSES: ExecutionStatus[] = ['queued', 'running', 'waiting', 'success', 'error', 'canceled', 'crashed'];
const ACTIVE = new Set(['queued', 'running', 'waiting']);

export function formatDuration(start: string, end?: string): string {
  if (!end) return '';
  const ms = Date.parse(end) - Date.parse(start);
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms < 1000) return `${ms} ms`;
  return ms < 60_000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.floor(ms / 60_000)} m ${Math.round((ms % 60_000) / 1000)} s`;
}

export function ExecutionsPage() {
  const [params, setParams] = useSearchParams();
  const status = params.getAll('status').filter((s): s is ExecutionStatus => (ALL_STATUSES as string[]).includes(s));
  const workflowId = params.get('workflowId') ?? undefined;
  const [live, setLive] = useState(true);
  const list = useExecutions({ status, workflowId }, live ? EXECUTIONS_REFRESH_MS : false);
  const wfs = useWorkflows({ q: '' });
  const m = useExecutionMutations();
  const toast = useToast();
  const names = new Map(wfs.data?.pages.flatMap((p) => p.data).map((w) => [w.id, w.name]) ?? []);
  const items = list.data?.pages.flatMap((p) => p.data) ?? [];

  const toggle = (s: ExecutionStatus) => {
    const next = status.includes(s) ? status.filter((x) => x !== s) : [...status, s];
    const np = new URLSearchParams(params);
    np.delete('status');
    next.forEach((x) => np.append('status', x));
    setParams(np, { replace: true });
  };

  return (
    <main className="page">
      <div className="page-inner">
        <div className="page-head">
          <h1>{t('nav.executions')}</h1>
          <span className="spacer" />
          <button className="chip" aria-pressed={live} onClick={() => setLive((v) => !v)}>{t('exec.live')}</button>
          <button className="btn" onClick={() => void list.refetch()}>{t('common.refresh')}</button>
        </div>
        <div className="toolbar" role="group" aria-label={t('exec.filterStatus')}>
          {ALL_STATUSES.map((s) => (
            <button key={s} className={`chip ${s}`} aria-pressed={status.includes(s)} onClick={() => toggle(s)}>{s}</button>
          ))}
          {workflowId && (
            <button className="chip" aria-pressed onClick={() => { const np = new URLSearchParams(params); np.delete('workflowId'); setParams(np, { replace: true }); }}>
              {names.get(workflowId) ?? workflowId} ✕
            </button>
          )}
        </div>
        <div className="card">
          {list.isPending ? <div className="empty"><span className="spinner" /></div>
            : list.isError ? <div className="empty" role="alert">{describeError(list.error)}</div>
            : items.length === 0 ? <div className="empty">{t('exec.empty')}</div> : (
              <table className="list">
                <thead>
                  <tr><th>{t('exec.status')}</th><th>{t('exec.workflow')}</th><th>{t('exec.mode')}</th><th>{t('exec.started')}</th><th>{t('exec.duration')}</th><th><span className="sr-only">{t('common.actions')}</span></th></tr>
                </thead>
                <tbody>
                  {items.map((e) => (
                    <tr key={e.id}>
                      <td><span className={`badge ${e.status}`}>{e.status}</span></td>
                      <td>
                        <Link to={`/workflows/${e.workflowId}?execution=${e.id}`}>{names.get(e.workflowId) ?? e.workflowId}</Link>
                        {e.error && <div className="field-hint" title={e.error.message}>{e.error.node ? `${e.error.node}: ` : ''}{e.error.message.slice(0, 80)}</div>}
                      </td>
                      <td>{e.mode}</td>
                      <td><time dateTime={e.startedAt}>{new Date(e.startedAt).toLocaleString()}</time></td>
                      <td>{formatDuration(e.startedAt, e.finishedAt)}</td>
                      <td>
                        <div className="row-actions">
                          <Link className="btn sm" to={`/workflows/${e.workflowId}?execution=${e.id}`}>{t('exec.open')}</Link>
                          {ACTIVE.has(e.status) ? (
                            <button className="btn sm" onClick={() => m.cancel.mutate(e.id, { onError: toast.error })}>{t('run.cancel')}</button>
                          ) : (
                            <>
                              {e.status === 'error' && (
                                <button className="btn sm" onClick={() => m.retry.mutate(e.id, { onError: toast.error, onSuccess: () => toast.info(t('exec.retried')) })}>{t('exec.retry')}</button>
                              )}
                              <button className="btn ghost icon sm" aria-label={t('exec.delete')} onClick={() => m.remove.mutate(e.id, { onError: toast.error })}><Icon name="trash" /></button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
        </div>
        {list.hasNextPage && <button className="btn" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>{t('common.loadMore')}</button>}
      </div>
    </main>
  );
}
