import type { Execution } from '../api/types';
import { TIMELINE_MIN_BAR_PCT } from '../config';
import { t, tn } from '../i18n/t';
import { formatDuration } from '../pages/ExecutionsPage';

export interface TimelineRow {
  key: string;
  node: string;
  runIndex: number;
  /** How many runs this node has in the execution. */
  runCount: number;
  status: string;
  attempts: number;
  itemsIn: number;
  itemsOut: number;
  durationMs?: number;
  /** Bar start and width as percentages of the execution span. */
  offsetPct: number;
  widthPct: number;
  error?: string;
}

const countItems = (branches?: unknown[][]): number => (branches ?? []).reduce((n, b) => n + (b?.length ?? 0), 0);

/** Every node run of an execution, ordered by start time (runs without one last), with bar geometry. */
export function timelineRows(execution: Execution): TimelineRow[] {
  const origin = Date.parse(execution.startedAt);
  const runs = Object.entries(execution.runData ?? {}).flatMap(([node, list]) =>
    list.map((r) => ({ node, r, at: r.startedAt ? Date.parse(r.startedAt) : NaN, runCount: list.length })),
  );
  runs.sort((a, b) => {
    const an = Number.isNaN(a.at), bn = Number.isNaN(b.at);
    if (an !== bn) return an ? 1 : -1;
    return (an ? 0 : a.at - b.at) || a.node.localeCompare(b.node) || a.r.runIndex - b.r.runIndex;
  });
  const end = Math.max(
    execution.finishedAt ? Date.parse(execution.finishedAt) : 0,
    ...runs.map((x) => (Number.isNaN(x.at) ? 0 : x.at + (x.r.durationMs ?? 0))),
  );
  const span = Number.isFinite(origin) && end > origin ? end - origin : 0;
  return runs.map(({ node, r, at, runCount }) => {
    const start = Number.isNaN(at) || span === 0 ? 0 : Math.max(0, at - origin);
    const width = span === 0 ? 100 : Math.max(((r.durationMs ?? 0) / span) * 100, TIMELINE_MIN_BAR_PCT);
    const offsetPct = Math.min((start / (span || 1)) * 100, 100 - Math.min(width, 100));
    return {
      key: `${node}#${r.runIndex}`,
      node,
      runIndex: r.runIndex,
      runCount,
      status: r.status,
      attempts: r.attempts ?? 1,
      itemsIn: countItems(r.input?.main),
      itemsOut: countItems(r.output?.main),
      durationMs: r.durationMs,
      offsetPct,
      widthPct: Math.min(width, 100),
      error: r.error?.message,
    };
  });
}

function ms(v?: number): string {
  if (v === undefined) return '';
  return formatDuration('1970-01-01T00:00:00Z', new Date(v).toISOString());
}

export function Timeline({ execution, onJump, onClose }: { execution: Execution; onJump: (node: string) => void; onClose: () => void }) {
  const rows = timelineRows(execution);
  const err = execution.error;
  return (
    <section className="timeline" aria-label={t('timeline.title')}>
      <div className="timeline-head">
        <strong>{t('timeline.title')}</strong>
        <button className="btn sm" onClick={() => void navigator.clipboard?.writeText(execution.id)} title={t('common.copy')}>
          <code>{execution.id}</code>
        </button>
        <span className={`badge ${execution.status}`}>{execution.status}</span>
        <span className="field-hint">{execution.mode} · {t('timeline.version', { v: execution.workflowVersion })}</span>
        <time className="field-hint" dateTime={execution.startedAt}>{new Date(execution.startedAt).toLocaleString()}</time>
        {execution.finishedAt && <span className="field-hint">{formatDuration(execution.startedAt, execution.finishedAt)}</span>}
        {execution.waitUntil && <span className="field-hint">{t('timeline.waitUntil', { at: new Date(execution.waitUntil).toLocaleString() })}</span>}
        {(execution.attempts ?? 1) > 1 && <span className="field-hint">{t('timeline.runAttempt', { n: execution.attempts ?? 1 })}</span>}
        <span className="spacer" />
        <button className="btn ghost sm" onClick={onClose}>{t('common.close')}</button>
      </div>
      {err && <div className="runbar-error">{err.node ? `${err.node}: ` : ''}{err.message}</div>}
      {rows.length === 0 ? <div className="field-hint">{t('timeline.empty')}</div> : (
        <ul className="timeline-list">
          {rows.map((r) => (
            <li key={r.key}>
              <button className="timeline-row" onClick={() => onJump(r.node)}>
                <span className="timeline-name">
                  {r.node}{r.runCount > 1 ? ` #${r.runIndex + 1}` : ''}
                </span>
                <span className={`badge ${r.status}`}>{r.status}</span>
                {r.attempts > 1 && <span className="field-hint">{tn('timeline.attempts', r.attempts)}</span>}
                <span className="field-hint">{t('timeline.items', { in: r.itemsIn, out: r.itemsOut })}</span>
                <span className="field-hint timeline-dur">{ms(r.durationMs)}</span>
                <span className="timeline-track" aria-hidden="true">
                  <span className={`timeline-bar ${r.status}`} style={{ left: `${r.offsetPct}%`, width: `${r.widthPct}%` }} />
                </span>
                {r.error && <span className="runbar-error timeline-error">{r.error}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
