import { describe, expect, it } from 'vitest';
import type { WorkflowDoc } from '../api/types';
import { commit, initHistory, redo, undo } from './history';
import { copyFragment, parseFragment, pasteFragment, removeNodes } from './model';
import {
  addNote, asNoteColor, cloneNotes, createNote, fitsNoteText, noteLabel, noteTextBytes, NOTE_DEFAULT_SIZE, NOTE_LABEL_CHARS, NOTE_MIN_SIZE, removeNotes,
  sanitizeNotes, setNoteBoxes, updateNote,
} from './notes';

const doc = (): WorkflowDoc => ({ name: 'w', nodes: [{ name: 'A', type: 't.a', position: [0, 0] }], connections: [] });

describe('notes model', () => {
  it('creates a note with defaults and staggers it off an occupied spot', () => {
    const first = createNote(doc(), [10, 20], 'nt_1');
    expect(first).toMatchObject({ id: 'nt_1', text: '', position: [10, 20], size: NOTE_DEFAULT_SIZE, color: 'yellow' });
    const d = addNote(doc(), first);
    expect(createNote(d, [10, 20], 'nt_2').position).not.toEqual([10, 20]);
  });

  it('updates text and colour and removes notes, dropping an emptied list', () => {
    let d = addNote(doc(), createNote(doc(), [0, 0], 'nt_1'));
    d = updateNote(d, 'nt_1', { text: '# hi', color: 'blue' });
    expect(d.notes?.[0]).toMatchObject({ text: '# hi', color: 'blue' });
    expect(removeNotes(d, ['nt_1']).notes).toBeUndefined();
    expect(removeNotes(d, ['other']).notes).toHaveLength(1);
  });

  it('moves and resizes, rounding and keeping the minimum size', () => {
    const d = addNote(doc(), createNote(doc(), [0, 0], 'nt_1'));
    const moved = setNoteBoxes(d, { nt_1: { position: [10.4, 20.6] } });
    expect(moved.notes?.[0]).toMatchObject({ position: [10, 21], size: NOTE_DEFAULT_SIZE });
    const big = setNoteBoxes(d, { nt_1: { position: [0, 0], size: { width: 400.4, height: 300 } } });
    expect(big.notes?.[0]?.size).toEqual({ width: 400, height: 300 });
    const tiny = setNoteBoxes(d, { nt_1: { position: [0, 0], size: { width: 5, height: 5 } } });
    expect(tiny.notes?.[0]?.size).toEqual(NOTE_MIN_SIZE);
    expect(setNoteBoxes(d, { nope: { position: [1, 1] } })).toBe(d);
  });

  it('does not touch notes when nodes are removed', () => {
    const d = addNote(doc(), createNote(doc(), [0, 0], 'nt_1'));
    expect(removeNodes(d, ['A']).notes).toHaveLength(1);
  });

  it('undoes and redoes note edits like node edits', () => {
    let h = initHistory(doc());
    h = commit(h, addNote(h.present, createNote(h.present, [0, 0], 'nt_1')));
    h = commit(h, updateNote(h.present, 'nt_1', { text: 'x' }));
    expect(h.present.notes?.[0]?.text).toBe('x');
    h = undo(h);
    expect(h.present.notes?.[0]?.text).toBe('');
    h = undo(h);
    expect(h.present.notes).toBeUndefined();
    h = redo(h);
    expect(h.present.notes).toHaveLength(1);
  });

  it('counts text in UTF-8 bytes, not characters', () => {
    expect(noteTextBytes('abc')).toBe(3);
    expect(noteTextBytes('é')).toBe(2);
    expect(noteTextBytes('界')).toBe(3);
    expect(noteTextBytes('🙂')).toBe(4);
  });

  it('refuses growth past the limit but allows shrinking', () => {
    expect(fitsNoteText('ab', 'abc', 3)).toBe(true);
    expect(fitsNoteText('abc', 'abcd', 3)).toBe(false);
    expect(fitsNoteText('ab', 'é界', 4)).toBe(false); // 5 bytes
    expect(fitsNoteText('ab', 'é界', 5)).toBe(true);
    // Already over the limit (the server lowered it): cutting down is allowed, typing more is not.
    expect(fitsNoteText('abcdef', 'abcde', 3)).toBe(true);
    expect(fitsNoteText('abcde', 'abcdef', 3)).toBe(false);
  });

  it('labels a note by its first line, cut to a fixed length, or by id', () => {
    expect(noteLabel({ ...createNote(doc(), [0, 0], 'nt_1'), text: '  Ship it\nlater' })).toBe('Ship it');
    expect(noteLabel({ ...createNote(doc(), [0, 0], 'nt_1'), text: 'x'.repeat(NOTE_LABEL_CHARS + 5) })).toHaveLength(NOTE_LABEL_CHARS);
    expect(noteLabel(createNote(doc(), [0, 0], 'nt_1'))).toBe('nt_1');
  });

  it('falls back to yellow for unknown colours', () => {
    expect(asNoteColor('green')).toBe('green');
    expect(asNoteColor('plaid')).toBe('yellow');
    expect(asNoteColor(undefined)).toBe('yellow');
  });
});

describe('notes in copy, paste and duplicate', () => {
  it('copies notes with nodes and pastes them with fresh ids and an offset', () => {
    const d = addNote(doc(), { ...createNote(doc(), [100, 100], 'nt_1'), text: 'keep', color: 'red' });
    const frag = copyFragment(d, ['A'], ['nt_1']);
    expect(frag.notes).toHaveLength(1);
    const r = pasteFragment(d, parseFragment(JSON.stringify(frag)) as NonNullable<ReturnType<typeof parseFragment>>);
    expect(r.names).toEqual(['A 1']);
    expect(r.noteIds).toHaveLength(1);
    expect(r.noteIds[0]).not.toBe('nt_1');
    expect(r.doc.notes).toHaveLength(2);
    expect(r.doc.notes?.[1]).toMatchObject({ text: 'keep', color: 'red', position: [140, 140] });
  });

  it('pastes a fragment that holds only notes', () => {
    const d = addNote(doc(), createNote(doc(), [0, 0], 'nt_1'));
    const frag = parseFragment(JSON.stringify(copyFragment(d, [], ['nt_1'])));
    expect(frag).not.toBeNull();
    const r = pasteFragment(doc(), frag as NonNullable<typeof frag>);
    expect(r.names).toEqual([]);
    expect(r.doc.notes).toHaveLength(1);
  });

  it('a copy without notes carries no notes key', () => {
    expect('notes' in copyFragment(doc(), ['A'])).toBe(false);
  });

  it('rejects malformed pasted notes', () => {
    expect(sanitizeNotes([{ id: 'x', position: [1], size: { width: 1, height: 1 } }, { id: 'y', position: [0, 0], size: { width: 0, height: 5 } }, null, 5])).toEqual([]);
    expect(parseFragment('{"notes":[{"position":"x"}]}')).toBeNull();
    expect(cloneNotes([{ id: 'a', position: [0, 0], size: { width: 1, height: 1 } }])[0]?.id).not.toBe('a');
  });
});
