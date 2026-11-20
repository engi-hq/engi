import { useMemo, useState } from 'react';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { fuzzyScore } from './paletteSearch';

export interface Command {
  id: string;
  label: string;
  shortcut?: string;
  run: () => void;
  enabled?: boolean;
}

/** Command palette: every editor action is reachable from here. */
export function CommandPalette({ commands, onClose }: { commands: Command[]; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [cursor, setCursor] = useState(0);
  const list = useMemo(
    () => commands.filter((c) => c.enabled !== false).map((c) => ({ c, s: fuzzyScore(q, c.label) })).filter((x) => x.s > 0).sort((a, b) => b.s - a.s).map((x) => x.c),
    [commands, q],
  );
  const run = (c?: Command) => { if (c) { onClose(); setTimeout(c.run, 0); } };
  return (
    <Dialog title={t('commands.title')} onClose={onClose}>
      <div
        style={{ display: 'grid', gap: 8 }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') { e.preventDefault(); setCursor((c) => Math.min(c + 1, list.length - 1)); }
          else if (e.key === 'ArrowUp') { e.preventDefault(); setCursor((c) => Math.max(0, c - 1)); }
          else if (e.key === 'Enter') { e.preventDefault(); run(list[cursor]); }
        }}
      >
        <input type="search" data-autofocus value={q} onChange={(e) => { setQ(e.target.value); setCursor(0); }} placeholder={t('commands.search')} aria-label={t('commands.search')} />
        <div role="listbox" aria-label={t('commands.title')} className="palette-list">
          {list.map((c, i) => (
            <div key={c.id} role="option" aria-selected={i === cursor} className={`palette-item${i === cursor ? ' active' : ''}`} onClick={() => run(c)} onMouseMove={() => setCursor(i)}>
              <span className="palette-text"><strong>{c.label}</strong></span>
              {c.shortcut && <kbd>{c.shortcut}</kbd>}
            </div>
          ))}
          {list.length === 0 && <div className="empty">{t('palette.none')}</div>}
        </div>
      </div>
    </Dialog>
  );
}

export const SHORTCUTS: Array<[string, string]> = [
  ['Tab / +', t('sc.palette')], ['Ctrl/Cmd+K', t('sc.commands')], ['Ctrl/Cmd+S', t('sc.save')], ['Ctrl/Cmd+Enter', t('sc.run')],
  ['Ctrl/Cmd+Z', t('sc.undo')], ['Ctrl/Cmd+Shift+Z', t('sc.redo')], ['Ctrl/Cmd+C / V / D', t('sc.copyPaste')],
  ['Delete', t('sc.delete')], ['D', t('sc.disable')], ['Enter', t('sc.open')], ['Arrows', t('sc.move')], ['C', t('sc.connect')], ['Ctrl/Cmd+Shift+L', t('sc.tidy')], ['N', t('sc.note')], ['H', t('sc.history')], ['?', t('sc.help')],
];

export function ShortcutHelp({ onClose }: { onClose: () => void }) {
  return (
    <Dialog title={t('sc.title')} onClose={onClose}>
      <table className="list">
        <tbody>
          {SHORTCUTS.map(([k, d]) => <tr key={k}><td><kbd>{k}</kbd></td><td>{d}</td></tr>)}
        </tbody>
      </table>
    </Dialog>
  );
}
