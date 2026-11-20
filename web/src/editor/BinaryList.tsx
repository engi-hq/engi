import { useEffect, useState } from 'react';
import { BINARY_PREVIEW_MAX_BYTES } from '../config';
import { t } from '../i18n/t';
import type { Item } from '../api/types';
import { binaryUrl, fetchBinaryText, humanSize, parseBinary, previewKind, type BinaryValue } from './binary';
import { Icon } from '../ui/Icon';

interface Entry {
  item: number;
  field: string;
  value: BinaryValue;
}

/** Binary values of the given items, in item then field order. `offset` is the index of the first item. */
export function binaryEntries(items: Item[], offset = 0): Entry[] {
  const out: Entry[] = [];
  items.forEach((it, i) => {
    for (const [field, raw] of Object.entries(it.binary ?? {})) {
      const value = parseBinary(raw);
      if (value) out.push({ item: offset + i, field, value });
    }
  });
  return out;
}

/** Name, type, size, download link and (where safe) a preview for each binary value of the items. */
export function BinaryList({ items, offset = 0, executionId }: { items: Item[]; offset?: number; executionId?: string }) {
  const entries = binaryEntries(items, offset);
  if (entries.length === 0) return null;
  return (
    <ul className="binary-list" aria-label={t('binary.title')} style={{ listStyle: 'none', padding: 0 }}>
      {entries.map((e) => (
        <li key={`${e.item}/${e.field}`}>
          <BinaryCard entry={e} executionId={executionId} />
        </li>
      ))}
    </ul>
  );
}

function BinaryCard({ entry, executionId }: { entry: Entry; executionId?: string }) {
  const { value, item, field } = entry;
  const kind = previewKind(value.mime);
  const download = binaryUrl(value.ref, executionId);
  const inline = binaryUrl(value.ref, executionId, true);
  const name = value.name || field;
  return (
    <div className="binary-card">
      <div className="binary-head">
        <Icon name="download" />
        <strong>{name}</strong>
        <span className="field-hint">{t('binary.where', { item, field })}</span>
        <span className="badge">{value.mime || t('binary.unknownType')}</span>
        {value.size > 0 && <span className="field-hint">{humanSize(value.size)}</span>}
        <span className="spacer" />
        {download
          ? <a className="btn sm" href={download} download={value.name || undefined} aria-label={t('binary.download', { name })}>{t('binary.downloadShort')}</a>
          : <span className="field-hint">{t('binary.noExecution')}</span>}
      </div>
      {inline && (
        <div className="binary-preview">
          {kind === 'image' && <img src={inline} alt={name} loading="lazy" />}
          {kind === 'audio' && <audio controls preload="none" src={inline} aria-label={name} />}
          {kind === 'video' && <video controls preload="none" src={inline} aria-label={name} />}
          {kind === 'text' && <TextPreview url={inline} size={value.size} />}
          {kind === 'pdf' && <span className="field-hint">{t('binary.pdfHint')}</span>}
        </div>
      )}
    </div>
  );
}

type TextState = { phase: 'idle' } | { phase: 'loading' } | { phase: 'done'; text: string; truncated: boolean } | { phase: 'error'; message: string };

/** Loads text on request and renders it as plain text, never as markup. */
function TextPreview({ url, size }: { url: string; size: number }) {
  const [state, setState] = useState<TextState>({ phase: 'idle' });
  const tooLarge = size > BINARY_PREVIEW_MAX_BYTES;

  useEffect(() => {
    if (state.phase !== 'loading') return;
    const ctl = new AbortController();
    fetchBinaryText(url, BINARY_PREVIEW_MAX_BYTES, ctl.signal)
      .then((r) => setState({ phase: 'done', ...r }))
      .catch((e: unknown) => { if (!ctl.signal.aborted) setState({ phase: 'error', message: e instanceof Error ? e.message : String(e) }); });
    return () => ctl.abort();
  }, [state.phase, url]);

  if (tooLarge) return <span className="field-hint">{t('binary.tooLarge', { max: humanSize(BINARY_PREVIEW_MAX_BYTES) })}</span>;
  switch (state.phase) {
    case 'idle': return <button className="btn sm" onClick={() => setState({ phase: 'loading' })}>{t('binary.preview')}</button>;
    case 'loading': return <span className="spinner" aria-label={t('common.loading')} />;
    case 'error': return <div className="callout error" role="alert">{t('binary.previewFailed', { message: state.message })}</div>;
    case 'done': return (
      <>
        <pre>{state.text}</pre>
        {state.truncated && <span className="field-hint">{t('binary.truncated', { max: humanSize(BINARY_PREVIEW_MAX_BYTES) })}</span>}
      </>
    );
  }
}
