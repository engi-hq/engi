import { describe, expect, it } from 'vitest';
import { commit, initHistory, redo, undo } from './history';

describe('history', () => {
  it('undoes and redoes snapshots', () => {
    let h = initHistory(1);
    h = commit(h, 2, undefined, 0);
    h = commit(h, 3, undefined, 0);
    h = undo(h);
    expect(h.present).toBe(2);
    h = undo(h);
    h = undo(h);
    expect(h.present).toBe(1);
    h = redo(h);
    expect(h.present).toBe(2);
  });
  it('drops the redo branch on a new commit', () => {
    let h = commit(commit(initHistory(1), 2, undefined, 0), 3, undefined, 0);
    h = undo(h);
    h = commit(h, 9, undefined, 0);
    expect(h.future).toEqual([]);
    expect(redo(h).present).toBe(9);
  });
  it('merges bursts with the same key into one undo step', () => {
    let h = initHistory('a');
    h = commit(h, 'ab', 'typing', 1000);
    h = commit(h, 'abc', 'typing', 1200);
    expect(h.past).toEqual(['a']);
    h = commit(h, 'abcd', 'typing', 5000);
    expect(h.past).toEqual(['a', 'abc']);
  });
  it('ignores a commit of the same value', () => {
    const h = initHistory({ a: 1 });
    expect(commit(h, h.present)).toBe(h);
  });
});
