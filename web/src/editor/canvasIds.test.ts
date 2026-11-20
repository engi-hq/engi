import { describe, expect, it } from 'vitest';
import { isNodeRfId, isNoteRfId, nodeNameOf, nodeRfId, noteIdOf, noteRfId } from './canvasIds';

describe('canvas ids', () => {
  it('round-trips node names and note ids', () => {
    expect(nodeNameOf(nodeRfId('Webhook'))).toBe('Webhook');
    expect(noteIdOf(noteRfId('nt_1'))).toBe('nt_1');
    expect(isNodeRfId(nodeRfId('A'))).toBe(true);
    expect(isNoteRfId(noteRfId('nt_1'))).toBe(true);
  });

  it('never gives a node and a note the same id, whatever the names contain', () => {
    const names = ['note:nt_1', 'nt_1', 'note:', 'node:note:nt_1', 'n', ''];
    const ids = new Set<string>();
    for (const name of names) ids.add(nodeRfId(name));
    for (const id of ['nt_1', '', 'note:nt_1']) ids.add(noteRfId(id));
    expect(ids.size).toBe(names.length + 3);
    // A node named like a note is still a node.
    expect(isNoteRfId(nodeRfId('note:nt_1'))).toBe(false);
    expect(isNodeRfId(noteRfId('node:x'))).toBe(false);
    expect(nodeNameOf(nodeRfId('note:nt_1'))).toBe('note:nt_1');
  });
});
