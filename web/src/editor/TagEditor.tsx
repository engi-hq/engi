import { useState, type KeyboardEvent } from 'react';
import { useTags } from '../api/accountHooks';
import { t } from '../i18n/t';

/** Chip editor for a workflow's tags: pick an existing tag or type a new name. */
export function TagEditor({ tags, onChange, readOnly }: { tags: string[]; onChange: (tags: string[]) => void; readOnly: boolean }) {
  const known = useTags();
  const [draft, setDraft] = useState('');

  const add = () => {
    const name = draft.trim();
    setDraft('');
    if (name && !tags.includes(name)) onChange([...tags, name]);
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') { e.preventDefault(); add(); }
  };

  return (
    <div className="tag-editor" role="group" aria-label={t('tags.editor')} style={{ display: 'flex', gap: 4, alignItems: 'center', flexWrap: 'wrap' }}>
      {tags.map((name) => (
        <span key={name} className="badge">
          {name}
          {!readOnly && (
            <button className="btn ghost sm" aria-label={t('tags.remove', { name })} onClick={() => onChange(tags.filter((x) => x !== name))}>×</button>
          )}
        </span>
      ))}
      {!readOnly && (
        <>
          <input
            list="engi-known-tags"
            value={draft}
            placeholder={t('tags.add')}
            aria-label={t('tags.add')}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={onKey}
            onBlur={add}
            style={{ width: 120 }}
          />
          <datalist id="engi-known-tags">
            {(known.data ?? []).filter((x) => !tags.includes(x.name)).map((x) => <option key={x.id} value={x.name} />)}
          </datalist>
        </>
      )}
    </div>
  );
}
