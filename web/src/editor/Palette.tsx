import { useMemo, useState } from 'react';
import type { NodeType } from '../api/types';
import { t } from '../i18n/t';
import { Dialog } from '../ui/Dialog';
import { Icon } from '../ui/Icon';
import { groupVar } from './NodeCard';
import { GROUP_ORDER, loadRecent, searchNodeTypes } from './paletteSearch';

interface Props {
  types: NodeType[];
  onPick: (nt: NodeType) => void;
  onClose: () => void;
}

/** Searchable node palette, opened with + or Tab. Types come from GET /node-types. */
export function Palette({ types, onPick, onClose }: Props) {
  const [query, setQuery] = useState('');
  const [cursor, setCursor] = useState(0);
  const recent = useMemo(loadRecent, []);
  const latest = useMemo(() => {
    const m = new Map<string, NodeType>();
    for (const nt of types) if ((m.get(nt.type)?.version ?? 0) < nt.version) m.set(nt.type, nt);
    return [...m.values()];
  }, [types]);
  const results = useMemo(() => searchNodeTypes(latest, query, recent), [latest, query, recent]);

  // Group when not searching; a flat ranked list when searching.
  const sections = useMemo(() => {
    if (query) return [{ group: '', items: results }];
    const rec = results.filter((r) => recent.includes(r.type)).sort((a, b) => recent.indexOf(a.type) - recent.indexOf(b.type));
    const rest = results.filter((r) => !rec.includes(r));
    const groups = [...new Set(rest.map((r) => r.group))].sort((a, b) => {
      const ia = GROUP_ORDER.indexOf(a);
      const ib = GROUP_ORDER.indexOf(b);
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
    });
    return [
      ...(rec.length ? [{ group: 'recent', items: rec }] : []),
      ...groups.map((g) => ({ group: g, items: rest.filter((r) => r.group === g) })),
    ];
  }, [results, query, recent]);
  const flat = sections.flatMap((s) => s.items);
  const active = flat[Math.min(cursor, flat.length - 1)];

  return (
    <Dialog title={t('palette.title')} onClose={onClose} wide>
      <div
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') { e.preventDefault(); setCursor((c) => Math.min(c + 1, flat.length - 1)); }
          else if (e.key === 'ArrowUp') { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)); }
          else if (e.key === 'Enter' && active) { e.preventDefault(); onPick(active); }
        }}
        style={{ display: 'grid', gap: 10 }}
      >
        <input
          type="search"
          value={query}
          data-autofocus
          onChange={(e) => { setQuery(e.target.value); setCursor(0); }}
          placeholder={t('palette.search')}
          aria-label={t('palette.search')}
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={active ? `pal-${active.type}` : undefined}
        />
        <div id="palette-list" role="listbox" className="palette-list" aria-label={t('palette.results')}>
          {flat.length === 0 && <div className="empty">{t('palette.none')}</div>}
          {sections.map((s) => (
            <div key={s.group || 'results'} role="group" aria-label={s.group ? groupLabel(s.group) : undefined}>
              {s.group && <div className="palette-group">{groupLabel(s.group)}</div>}
              {s.items.map((nt) => (
                <div
                  key={nt.type}
                  id={`pal-${nt.type}`}
                  role="option"
                  aria-selected={nt === active}
                  className={`palette-item${nt === active ? ' active' : ''}`}
                  onClick={() => onPick(nt)}
                  onMouseMove={() => setCursor(flat.indexOf(nt))}
                >
                  <span className="ncard-icon" style={{ color: groupVar(nt.group) }}><Icon name={nt.icon ?? 'workflow'} size={16} /></span>
                  <span className="palette-text">
                    <strong>{nt.displayName}</strong>
                    <span className="field-hint">{nt.description}</span>
                  </span>
                  <span className="badge">{groupLabel(nt.group)}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
      </div>
    </Dialog>
  );
}

function groupLabel(g: string): string {
  const known: Record<string, string> = {
    recent: t('palette.recent'), trigger: t('group.trigger'), action: t('group.action'), transform: t('group.transform'),
    flow: t('group.flow'), ai: t('group.ai'), code: t('group.code'),
  };
  return known[g] ?? g;
}
