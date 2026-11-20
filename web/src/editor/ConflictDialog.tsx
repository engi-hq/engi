import type { WfNote, WorkflowDoc } from '../api/types';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { noteLabel, notesOf } from './notes';

export interface DocDiff {
  added: string[];
  removed: string[];
  changed: string[];
  connectionsChanged: boolean;
  /** Sticky notes, listed by their first line (or id) like nodes are by name. */
  notesAdded: string[];
  notesRemoved: string[];
  notesChanged: string[];
}

/** Summary of what differs between my copy and the server's latest, by node name and by note id. */
export function diffDocs(mine: WorkflowDoc, theirs: WorkflowDoc): DocDiff {
  const a = new Map(mine.nodes.map((n) => [n.name, JSON.stringify(n)]));
  const b = new Map(theirs.nodes.map((n) => [n.name, JSON.stringify(n)]));
  const key = (d: WorkflowDoc) => JSON.stringify(d.connections.map((c) => `${c.from.node}:${c.from.index}>${c.to.node}:${c.to.index}`).sort());
  const notesA = new Map(notesOf(mine).map((n) => [n.id, n]));
  const notesB = new Map(notesOf(theirs).map((n) => [n.id, n]));
  const label = (n: WfNote | undefined) => (n ? noteLabel(n) : '');
  return {
    added: [...a.keys()].filter((k) => !b.has(k)),
    removed: [...b.keys()].filter((k) => !a.has(k)),
    changed: [...a.keys()].filter((k) => b.has(k) && a.get(k) !== b.get(k)),
    connectionsChanged: key(mine) !== key(theirs),
    notesAdded: [...notesA.values()].filter((n) => !notesB.has(n.id)).map(label),
    notesRemoved: [...notesB.values()].filter((n) => !notesA.has(n.id)).map(label),
    notesChanged: [...notesA.values()]
      .filter((n) => notesB.has(n.id) && JSON.stringify(n) !== JSON.stringify(notesB.get(n.id)))
      .map(label),
  };
}

interface Props {
  diff: DocDiff;
  latestVersion: number;
  onOverwrite: () => void;
  onLoadLatest: () => void;
  onClose: () => void;
}

export function ConflictDialog({ diff, latestVersion, onOverwrite, onLoadLatest, onClose }: Props) {
  const list = (label: string, names: string[]) => names.length > 0 && <li><strong>{label}:</strong> {names.join(', ')}</li>;
  return (
    <Dialog title={t('conflict.title')} onClose={onClose}>
      <p style={{ margin: 0 }}>{t('conflict.body', { v: latestVersion })}</p>
      <ul style={{ margin: 0 }}>
        {list(t('conflict.mineOnly'), diff.added)}
        {list(t('conflict.theirsOnly'), diff.removed)}
        {list(t('conflict.differ'), diff.changed)}
        {diff.connectionsChanged && <li>{t('conflict.connections')}</li>}
        {list(t('conflict.notesMineOnly'), diff.notesAdded)}
        {list(t('conflict.notesTheirsOnly'), diff.notesRemoved)}
        {list(t('conflict.notesDiffer'), diff.notesChanged)}
      </ul>
      <div className="dialog-actions">
        <button className="btn" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn" onClick={onLoadLatest}>{t('conflict.loadLatest')}</button>
        <button className="btn primary" data-autofocus onClick={onOverwrite}>{t('conflict.overwrite')}</button>
      </div>
    </Dialog>
  );
}
