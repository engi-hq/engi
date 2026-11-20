import { useState } from 'react';
import { t } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { parsePinned } from './model';

interface Props {
  /** Items of the node's last output that "Pin output" would store. */
  output: object[];
  pinned?: object[];
  readOnly: boolean;
  onPin: (items: object[] | undefined) => void;
}

/** Pin, edit and unpin a node's output. Pinned data is saved with the workflow and replaces the node's output in runs. */
export function PinControls({ output, pinned, readOnly, onPin }: Props) {
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState('');
  const [error, setError] = useState('');

  const startEdit = () => { setText(JSON.stringify(pinned ?? output, null, 2)); setError(''); setEditing(true); };
  const save = () => {
    const r = parsePinned(text);
    if (!r.ok) { setError(t(`pin.error.${r.reason}`)); return; }
    onPin(r.items);
    setEditing(false);
  };

  if (editing) {
    return (
      <div className="pin-editor">
        <textarea
          value={text}
          aria-label={t('pin.json')}
          aria-invalid={error ? true : undefined}
          spellCheck={false}
          onChange={(e) => { setText(e.target.value); setError(''); }}
        />
        {error && <div className="field-error" role="alert">{error}</div>}
        <div className="toolbar">
          <button className="btn sm primary" onClick={save}>{t('pin.save')}</button>
          <button className="btn sm" onClick={() => setEditing(false)}>{t('pin.cancel')}</button>
        </div>
      </div>
    );
  }
  return (
    <div className="toolbar">
      {pinned && <span className="field-hint"><Icon name="pin" size={12} /> {t('pin.pinned')}</span>}
      {!readOnly && (
        <>
          {!pinned && <button className="btn sm" disabled={output.length === 0} onClick={() => onPin(output)}>{t('pin.pin')}</button>}
          <button className="btn sm" onClick={startEdit}>{t('pin.edit')}</button>
          {pinned && <button className="btn sm" onClick={() => onPin(undefined)}>{t('pin.unpin')}</button>}
        </>
      )}
    </div>
  );
}
