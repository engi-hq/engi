import { describe, expect, it } from 'vitest';
import type { WorkflowDoc } from '../api/types';
import { commit, initHistory, undo } from './history';
import { LAYOUT_CONFIG as C, autoLayout } from './layout';

const mk = (nodes: Array<[string, number, number]>, conns: Array<[string, string, number?]>): WorkflowDoc => ({
  name: 'w',
  nodes: nodes.map(([name, x, y]) => ({ name, type: 't', position: [x, y] })),
  connections: conns.map(([f, t, i = 0]) => ({ from: { node: f, port: 'main', index: i }, to: { node: t, port: 'main', index: 0 } })),
} as WorkflowDoc);
const pos = (d: WorkflowDoc, n: string) => d.nodes.find((x) => x.name === n)!.position!;

describe('autoLayout', () => {
  it('layers a linear flow left to right', () => {
    const d = autoLayout(mk([['A', 500, 500], ['B', 0, 0], ['C', 9, 9]], [['A', 'B'], ['B', 'C']]));
    expect([pos(d, 'A')[0], pos(d, 'B')[0], pos(d, 'C')[0]]).toEqual([0, C.columnWidth, 2 * C.columnWidth]);
    expect(new Set(['A', 'B', 'C'].map((n) => pos(d, n)[1])).size).toBe(1);
  });
  it('stacks IF outputs in port order', () => {
    const d = autoLayout(mk([['If', 0, 0], ['F', 300, 0], ['T', 300, 300]], [['If', 'F', 1], ['If', 'T', 0]]));
    expect(pos(d, 'T')[1]).toBeLessThan(pos(d, 'F')[1]);
  });
  it('keeps the existing vertical order of siblings on the same port', () => {
    const d = autoLayout(mk([['S', 0, 0], ['Lo', 300, 400], ['Hi', 300, 50]], [['S', 'Lo'], ['S', 'Hi']]));
    expect(pos(d, 'Hi')[1]).toBeLessThan(pos(d, 'Lo')[1]);
  });
  it('places a merge after its latest input', () => {
    const d = autoLayout(mk([['S', 0, 0], ['A', 0, 0], ['B', 0, 0], ['M', 0, 0]], [['S', 'A'], ['A', 'B'], ['S', 'M'], ['B', 'M']]));
    expect(pos(d, 'M')[0]).toBe(3 * C.columnWidth);
  });
  it('terminates on loops and ignores the back edge', () => {
    const d = autoLayout(mk([['A', 0, 0], ['B', 0, 0], ['C', 0, 0]], [['A', 'B'], ['B', 'C'], ['C', 'B']]));
    expect([pos(d, 'A')[0], pos(d, 'B')[0], pos(d, 'C')[0]]).toEqual([0, C.columnWidth, 2 * C.columnWidth]);
    expect(() => autoLayout(mk([['X', 0, 0], ['Y', 0, 0]], [['X', 'Y'], ['Y', 'X']]))).not.toThrow();
  });
  it('puts disconnected nodes below the graph', () => {
    const d = autoLayout(mk([['A', 0, 0], ['B', 0, 0], ['Z', 0, 0]], [['A', 'B']]));
    expect(pos(d, 'Z')[1]).toBeGreaterThan(pos(d, 'A')[1]);
  });
  it('lays out only a 2+ node selection, anchored at its corner', () => {
    const doc = mk([['A', 0, 0], ['B', 700, 300], ['C', 900, 900]], [['A', 'B'], ['B', 'C']]);
    const d = autoLayout(doc, ['B', 'C']);
    expect(pos(d, 'A')).toEqual([0, 0]);
    expect(pos(d, 'B')).toEqual([700, 300]);
    expect(pos(d, 'C')).toEqual([700 + C.columnWidth, 300]);
    expect(autoLayout(doc, ['B'])).not.toBe(doc); // single selection -> whole workflow
    expect(pos(autoLayout(doc, ['B']), 'A')).toEqual([0, 0]);
  });
  it('moves a sticky note with the node it overlaps most and leaves the rest', () => {
    const note = (id: string, x: number, y: number) => ({ id, text: '', position: [x, y] as [number, number], size: { width: 100, height: 60 } });
    // A stays in column 0 (its place is the top-left of the layout), B moves 120 px left into column 1.
    const doc = { ...mk([['A', 500, 500], ['B', 900, 500]], [['A', 'B']]), notes: [note('onB', 905, 505), note('onA', 520, 520), note('far', 5000, 0)] };
    const d = autoLayout(doc);
    expect(pos(d, 'B')).toEqual([780, 500]);
    expect(d.notes?.find((n) => n.id === 'onB')?.position).toEqual([785, 505]);
    expect(d.notes?.find((n) => n.id === 'onA')?.position).toEqual([520, 520]);
    expect(d.notes?.find((n) => n.id === 'far')?.position).toEqual([5000, 0]);
  });

  it('moves notes only with the nodes of a selection', () => {
    const note = (id: string, x: number, y: number) => ({ id, text: '', position: [x, y] as [number, number], size: { width: 100, height: 60 } });
    const doc = { ...mk([['A', 0, 0], ['B', 700, 300], ['C', 900, 900]], [['A', 'B'], ['B', 'C']]), notes: [note('onC', 905, 905)] };
    const d = autoLayout(doc, ['B', 'C']);
    expect(pos(d, 'C')).toEqual([700 + C.columnWidth, 300]);
    expect(d.notes?.[0]?.position).toEqual([905 + (700 + C.columnWidth - 900), 905 + (300 - 900)]);
  });

  it('is one undo step that restores positions', () => {
    const doc = mk([['A', 10, 10], ['B', 50, 400]], [['A', 'B']]);
    const h = commit(initHistory(doc), autoLayout(doc));
    expect(h.past).toHaveLength(1);
    expect(undo(h).present).toBe(doc);
  });
});
