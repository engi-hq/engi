import type { WfNote, WorkflowDoc } from '../api/types';

/** The fixed sticky-note palette. Each name maps to `--note-<name>-bg` and `--note-<name>-border` theme tokens. */
export const NOTE_COLORS = ['yellow', 'blue', 'green', 'red', 'purple', 'gray'] as const;
export type NoteColor = (typeof NOTE_COLORS)[number];
export const DEFAULT_NOTE_COLOR: NoteColor = 'yellow';

export const NOTE_DEFAULT_SIZE = { width: 240, height: 160 };
export const NOTE_MIN_SIZE = { width: 100, height: 60 };
/** How far a pasted or duplicated note is shifted from the original. */
export const NOTE_PASTE_OFFSET: [number, number] = [40, 40];

const utf8 = new TextEncoder();

/** Size of a note text in UTF-8 bytes, the unit the server limits (workflow.maxNoteTextBytes). */
export const noteTextBytes = (text: string): number => utf8.encode(text).byteLength;

/**
 * Whether an edit from `prev` to `next` may be kept. Growing past the limit is refused; shrinking is always allowed,
 * so a text that is already over the limit (after the server lowered it) can still be cut down.
 */
export const fitsNoteText = (prev: string, next: string, maxBytes: number): boolean => {
  const size = noteTextBytes(next);
  return size <= maxBytes || size <= noteTextBytes(prev);
};

/** Characters of a note's first line shown as its name in lists such as the conflict dialog. */
export const NOTE_LABEL_CHARS = 40;

/** Short name of a note for lists: its first line, or its id when empty. */
export const noteLabel = (note: WfNote): string => (note.text ?? '').trim().split('\n')[0]?.slice(0, NOTE_LABEL_CHARS).trim() || note.id;

export const asNoteColor = (c: string | undefined): NoteColor => (NOTE_COLORS as readonly string[]).includes(c ?? '') ? (c as NoteColor) : DEFAULT_NOTE_COLOR;

export function newNoteId(): string {
  const rnd = typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID().replace(/-/g, '') : Math.random().toString(36).slice(2) + Date.now().toString(36);
  return `nt_${rnd.slice(0, 20)}`;
}

export const notesOf = (doc: WorkflowDoc): WfNote[] => doc.notes ?? [];

/** Builds a note at `position`, staggered down so it does not hide another note placed at the same spot. */
export function createNote(doc: WorkflowDoc, position: [number, number], id: string = newNoteId()): WfNote {
  const taken = notesOf(doc);
  let [x, y] = position;
  while (taken.some((n) => n.position[0] === x && n.position[1] === y)) { x += NOTE_PASTE_OFFSET[0]; y += NOTE_PASTE_OFFSET[1]; }
  return { id, text: '', position: [x, y], size: { ...NOTE_DEFAULT_SIZE }, color: DEFAULT_NOTE_COLOR };
}

export function addNote(doc: WorkflowDoc, note: WfNote): WorkflowDoc {
  return { ...doc, notes: [...notesOf(doc), note] };
}

export function updateNote(doc: WorkflowDoc, id: string, patch: Partial<Omit<WfNote, 'id'>>): WorkflowDoc {
  return { ...doc, notes: notesOf(doc).map((n) => (n.id === id ? { ...n, ...patch } : n)) };
}

/** Removes notes; an emptied list is dropped so documents without notes stay as they were. */
export function removeNotes(doc: WorkflowDoc, ids: string[]): WorkflowDoc {
  const gone = new Set(ids);
  const notes = notesOf(doc).filter((n) => !gone.has(n.id));
  return { ...doc, notes: notes.length > 0 ? notes : undefined };
}

export interface NoteBox {
  position: [number, number];
  size?: { width: number; height: number };
}

/** Applies moves and resizes; sizes are rounded and never go below the minimum. */
export function setNoteBoxes(doc: WorkflowDoc, boxes: Record<string, NoteBox>): WorkflowDoc {
  if (!notesOf(doc).some((n) => boxes[n.id])) return doc;
  return {
    ...doc,
    notes: notesOf(doc).map((n) => {
      const b = boxes[n.id];
      if (!b) return n;
      const size = b.size
        ? { width: Math.max(NOTE_MIN_SIZE.width, Math.round(b.size.width)), height: Math.max(NOTE_MIN_SIZE.height, Math.round(b.size.height)) }
        : n.size;
      return { ...n, position: [Math.round(b.position[0]), Math.round(b.position[1])], size };
    }),
  };
}

/** Copies of the given notes with fresh ids, shifted by `offset`. */
export function cloneNotes(notes: WfNote[], offset: [number, number] = NOTE_PASTE_OFFSET): WfNote[] {
  return notes.map((n) => ({ ...n, id: newNoteId(), position: [n.position[0] + offset[0], n.position[1] + offset[1]], size: { ...n.size } }));
}

/** Keeps only well-formed notes from untrusted input (pasted clipboard text). */
export function sanitizeNotes(v: unknown): WfNote[] {
  if (!Array.isArray(v)) return [];
  const out: WfNote[] = [];
  for (const raw of v as Partial<WfNote>[]) {
    if (!raw || typeof raw !== 'object') continue;
    const pos = raw.position;
    const size = raw.size;
    if (!Array.isArray(pos) || pos.length !== 2 || !pos.every((p) => Number.isFinite(p))) continue;
    if (!size || !Number.isFinite(size.width) || !Number.isFinite(size.height) || size.width <= 0 || size.height <= 0) continue;
    out.push({
      id: typeof raw.id === 'string' ? raw.id : '',
      text: typeof raw.text === 'string' ? raw.text : '',
      position: [pos[0] as number, pos[1] as number],
      size: { width: size.width, height: size.height },
      color: asNoteColor(raw.color),
    });
  }
  return out;
}
