import { NodeResizer, type Node, type NodeProps } from '@xyflow/react';
import { memo, useEffect, useRef, useState } from 'react';
import type { WfNote } from '../api/types';
import { t } from '../i18n/t';
import { Icon } from '../ui/Icon';
import { Markdown } from './Markdown';
import { asNoteColor, fitsNoteText, NOTE_COLORS, NOTE_MIN_SIZE, noteTextBytes, type NoteBox } from './notes';
import './notes.css';

export interface NoteData extends Record<string, unknown> {
  note: WfNote;
  readOnly: boolean;
  /** Largest note text the server accepts, in UTF-8 bytes. */
  maxTextBytes: number;
  onPatch: (id: string, patch: Partial<Omit<WfNote, 'id'>>, key?: string) => void;
  onBox: (id: string, box: NoteBox) => void;
  onRemove: (id: string) => void;
}
export type NoteNode = Node<NoteData, 'note'>;

interface ViewProps {
  note: WfNote;
  selected: boolean;
  readOnly: boolean;
  maxTextBytes: number;
  onPatch: NoteData['onPatch'];
  onRemove: NoteData['onRemove'];
}

/** The note's face: rendered markdown, a textarea with a byte counter while editing, and colour and delete controls while selected. */
export function NoteView({ note, selected, readOnly, maxTextBytes, onPatch, onRemove }: ViewProps) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(note.text ?? '');
  const area = useRef<HTMLTextAreaElement>(null);
  const color = asNoteColor(note.color);

  useEffect(() => { if (editing) area.current?.focus(); }, [editing]);
  useEffect(() => { if (!editing) setDraft(note.text ?? ''); }, [note.text, editing]);

  const finish = () => {
    setEditing(false);
    if (draft !== (note.text ?? '')) onPatch(note.id, { text: draft });
  };

  return (
    <div
      className="note"
      data-color={color}
      data-selected={selected || undefined}
      role="note"
      style={{ background: `var(--note-${color}-bg)`, borderColor: `var(--note-${color}-border)` }}
      onDoubleClick={(e) => { if (readOnly) return; e.stopPropagation(); setEditing(true); }}
    >
      {selected && !readOnly && !editing && (
        <div className="note-toolbar nodrag" role="toolbar" aria-label={t('note.toolbar')}>
          {NOTE_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              className="note-swatch"
              style={{ background: `var(--note-${c}-bg)`, borderColor: `var(--note-${c}-border)` }}
              aria-label={t('note.color', { color: t(`note.color.${c}`) })}
              aria-pressed={c === color}
              title={t(`note.color.${c}`)}
              onClick={() => onPatch(note.id, { color: c })}
            />
          ))}
          <button type="button" className="btn ghost icon sm" aria-label={t('note.delete')} title={t('note.delete')} onClick={() => onRemove(note.id)}>
            <Icon name="trash" />
          </button>
        </div>
      )}
      {editing && (
        <div className="note-count nodrag" data-over={noteTextBytes(draft) > maxTextBytes || undefined} aria-live="polite">
          {t('note.bytes', { used: noteTextBytes(draft), max: maxTextBytes })}
        </div>
      )}
      {editing ? (
        <textarea
          ref={area}
          className="note-edit nodrag nowheel nopan"
          value={draft}
          aria-label={t('note.edit')}
          placeholder={t('note.placeholder')}
          onChange={(e) => { if (fitsNoteText(draft, e.target.value, maxTextBytes)) setDraft(e.target.value); }}
          onBlur={finish}
          onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); e.currentTarget.blur(); } }}
        />
      ) : note.text?.trim() ? (
        <Markdown source={note.text} className="note-body nowheel" />
      ) : (
        <p className="note-empty">{readOnly ? '' : t('note.empty')}</p>
      )}
    </div>
  );
}

/** React Flow node for a sticky note: draggable, resizable from its corners when selected. */
function NoteNodeImpl({ data, selected }: NodeProps<NoteNode>) {
  const { note, readOnly, maxTextBytes, onPatch, onBox, onRemove } = data;
  return (
    <>
      <NodeResizer
        isVisible={selected && !readOnly}
        minWidth={NOTE_MIN_SIZE.width}
        minHeight={NOTE_MIN_SIZE.height}
        onResizeEnd={(_, p) => onBox(note.id, { position: [p.x, p.y], size: { width: p.width, height: p.height } })}
      />
      <NoteView note={note} selected={selected} readOnly={readOnly} maxTextBytes={maxTextBytes} onPatch={onPatch} onRemove={onRemove} />
    </>
  );
}

export const NoteCard = memo(NoteNodeImpl);
