import { useMemo, useState } from 'react';
import type { Item } from '../api/types';
import { t, tn } from '../i18n/t';
import { BinaryList } from './BinaryList';

const PAGE = 25;
const MAX_COLS = 24;

export function cellText(v: unknown): string {
  if (v === null) return 'null';
  if (v === undefined) return '';
  if (typeof v === 'string') return v;
  if (typeof v === 'object') {
    const s = JSON.stringify(v);
    return s.length > 120 ? `${s.slice(0, 117)}...` : s;
  }
  return String(v);
}

/** Column names of a list of items: union of top-level keys in first-seen order. */
export function columnsOf(items: Item[]): { cols: string[]; truncated: boolean } {
  const seen = new Set<string>();
  for (const it of items) for (const k of Object.keys(it.json ?? {})) seen.add(k);
  const all = [...seen];
  return { cols: all.slice(0, MAX_COLS), truncated: all.length > MAX_COLS };
}

interface Props {
  items: Item[];
  /** Accessible name of the region, e.g. "Output of Fetch". */
  label: string;
  /** Execution the items belong to; needed to build binary download links. */
  executionId?: string;
}

/** Table and JSON views of an item list, paged. */
export function DataView({ items, label, executionId }: Props) {
  const [mode, setMode] = useState<'table' | 'json'>('table');
  const [page, setPage] = useState(0);
  const pages = Math.max(1, Math.ceil(items.length / PAGE));
  const p = Math.min(page, pages - 1);
  const slice = items.slice(p * PAGE, p * PAGE + PAGE);
  return (
    <section className="dataview" aria-label={label}>
      <div className="toolbar">
        <div className="seg" role="group" aria-label={t('data.view')}>
          <button type="button" aria-pressed={mode === 'table'} onClick={() => setMode('table')}>{t('data.table')}</button>
          <button type="button" aria-pressed={mode === 'json'} onClick={() => setMode('json')}>{t('data.json')}</button>
        </div>
        <span className="field-hint">{tn('data.itemCount', items.length)}</span>
        <span className="spacer" />
        {pages > 1 && (
          <>
            <button className="btn sm" disabled={p === 0} onClick={() => setPage(p - 1)} aria-label={t('data.prev')}>‹</button>
            <span className="field-hint">{p + 1} / {pages}</span>
            <button className="btn sm" disabled={p >= pages - 1} onClick={() => setPage(p + 1)} aria-label={t('data.next')}>›</button>
          </>
        )}
      </div>
      {items.length === 0 ? (
        <div className="empty">{t('data.noItems')}</div>
      ) : mode === 'table' ? (
        <TableView items={slice} offset={p * PAGE} />
      ) : (
        <JsonView value={slice.map((i) => i.json)} />
      )}
      <BinaryList items={slice} offset={p * PAGE} executionId={executionId} />
    </section>
  );
}

function TableView({ items, offset }: { items: Item[]; offset: number }) {
  const { cols, truncated } = useMemo(() => columnsOf(items), [items]);
  return (
    <div className="data-scroll">
      <table className="list data-table">
        <thead>
          <tr>
            <th scope="col">#</th>
            {cols.map((c) => <th key={c} scope="col">{c}</th>)}
          </tr>
        </thead>
        <tbody>
          {items.map((it, i) => (
            <tr key={offset + i}>
              <th scope="row">{offset + i}</th>
              {cols.map((c) => {
                const v = it.json?.[c];
                return <td key={c} title={typeof v === 'object' ? JSON.stringify(v) : undefined}>{cellText(v)}</td>;
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {truncated && <div className="field-hint">{t('data.moreColumns', { n: MAX_COLS })}</div>}
    </div>
  );
}

function matchesFilter(key: string, v: unknown, f: string): boolean {
  if (key.toLowerCase().includes(f)) return true;
  if (v === null || typeof v !== 'object') return String(v).toLowerCase().includes(f);
  return Object.entries(v as object).some(([k, x]) => matchesFilter(k, x, f));
}

/** Collapsible, searchable JSON tree. */
export function JsonView({ value }: { value: unknown }) {
  const [filter, setFilter] = useState('');
  const f = filter.trim().toLowerCase();
  return (
    <div className="json-view">
      <input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t('data.search')} aria-label={t('data.search')} />
      <div className="json-tree" role="tree" aria-label={t('data.json')}>
        <Node k="" v={value} depth={0} filter={f} root />
      </div>
    </div>
  );
}

function Node({ k, v, depth, filter, root }: { k: string; v: unknown; depth: number; filter: string; root?: boolean }) {
  if (filter && !root && !matchesFilter(k, v, filter)) return null;
  const label = root ? null : <span className="jk">{k}: </span>;
  if (v === null || typeof v !== 'object') {
    const cls = typeof v === 'string' ? 'js' : v === null ? 'jn' : 'jv';
    return (
      <div className="jrow" role="treeitem" aria-selected="false">
        {label}
        <span className={cls}>{typeof v === 'string' ? JSON.stringify(v) : String(v)}</span>
      </div>
    );
  }
  const entries = Array.isArray(v) ? v.map((x, i) => [String(i), x] as const) : Object.entries(v as object);
  const open = filter ? true : depth < 2;
  return (
    <details open={open} className="jnode" key={`${k}-${filter}`}>
      <summary role="treeitem" aria-expanded={open}>
        {label}
        <span className="field-hint">{Array.isArray(v) ? `[${entries.length}]` : `{${entries.length}}`}</span>
      </summary>
      <div className="jchildren" role="group">
        {entries.map(([ck, cv]) => <Node key={ck} k={ck} v={cv} depth={depth + 1} filter={filter} />)}
      </div>
    </details>
  );
}
