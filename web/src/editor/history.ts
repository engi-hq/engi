/** Undo/redo as a command stack of immutable snapshots. */
export interface History<T> {
  past: T[];
  present: T;
  future: T[];
  /** Key of the last commit, used to merge bursts (typing, dragging) into one undo step. */
  lastKey?: string;
  lastAt: number;
}

export const MERGE_WINDOW_MS = 800;
const LIMIT = 200;

export const initHistory = <T>(present: T): History<T> => ({ past: [], present, future: [], lastAt: 0 });

export function commit<T>(h: History<T>, next: T, key?: string, now = Date.now()): History<T> {
  if (Object.is(next, h.present)) return h;
  const merge = key !== undefined && key === h.lastKey && now - h.lastAt < MERGE_WINDOW_MS;
  const past = merge ? h.past : [...h.past, h.present].slice(-LIMIT);
  return { past, present: next, future: [], lastKey: key, lastAt: now };
}

export function undo<T>(h: History<T>): History<T> {
  const prev = h.past[h.past.length - 1];
  if (prev === undefined) return h;
  return { past: h.past.slice(0, -1), present: prev, future: [h.present, ...h.future], lastAt: 0 };
}

export function redo<T>(h: History<T>): History<T> {
  const next = h.future[0];
  if (next === undefined) return h;
  return { past: [...h.past, h.present], present: next, future: h.future.slice(1), lastAt: 0 };
}

/** Replace the present without recording history (after a save assigns version/ids). */
export const reset = <T>(h: History<T>, present: T): History<T> => ({ ...h, present });
