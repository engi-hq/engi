import { describe, expect, it } from 'vitest';
import type { NodeType, WorkflowDoc } from '../api/types';
import {
  addConnection, addNode, checkConnection, copyFragment, createNode, firstFreeOutput, freePosition, indexTypes, parseFragment, pasteFragment,
  removeConnection, removeNodes, renameNode, uniqueName,
} from './model';

const nt = (type: string, inputs: string[], outputs: Array<[string, string]>): NodeType => ({
  type, version: 1, displayName: type.split('.').pop() as string, group: 'flow',
  inputs: inputs.map((k) => ({ name: 'main', kind: k })),
  outputs: outputs.map(([name, kind]) => ({ name, kind })),
  properties: [{ name: 'mode', type: 'string', default: 'a' }], credentials: [],
});
const types = indexTypes([
  nt('t.trigger', [], [['main', 'main']]),
  nt('t.if', ['main'], [['true', 'main'], ['false', 'main']]),
  nt('t.merge', ['main', 'main'], [['main', 'main']]),
  nt('t.model', [], [['model', 'ai.model']]),
]);

const base = (): WorkflowDoc => ({
  name: 'wf',
  nodes: [
    { name: 'Start', type: 't.trigger', position: [0, 0] },
    { name: 'IF', type: 't.if', position: [200, 0] },
    { name: 'Merge', type: 't.merge', position: [400, 0] },
  ],
  connections: [{ from: { node: 'Start', port: 'main', index: 0 }, to: { node: 'IF', port: 'main', index: 0 } }],
  pinData: { IF: [{ json: {} }] },
});

describe('names', () => {
  it('generates unique names', () => {
    expect(uniqueName('IF', ['IF'])).toBe('IF 1');
    expect(uniqueName('IF 1', ['IF', 'IF 1'])).toBe('IF 2');
    expect(uniqueName('Set', ['IF'])).toBe('Set');
  });
  it('creates nodes with defaults and a unique name', () => {
    const doc = base();
    const n = createNode(types.get('t.if') as NodeType, doc, [1, 2]);
    expect(n.name).toBe('if');
    const n2 = createNode(types.get('t.if') as NodeType, addNode(doc, { ...n, name: 'if' }), [1, 2]);
    expect(n2.name).toBe('if 1');
    expect(n2.parameters).toEqual({ mode: 'a' });
  });
  it('renames and rewires connections and pin data', () => {
    const r = renameNode(base(), 'IF', 'Check');
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.doc.connections[0]?.to.node).toBe('Check');
    expect(Object.keys(r.doc.pinData ?? {})).toEqual(['Check']);
  });
  it('refuses empty and duplicate names', () => {
    expect(renameNode(base(), 'IF', '  ')).toEqual({ ok: false, reason: 'empty' });
    expect(renameNode(base(), 'IF', 'Merge')).toEqual({ ok: false, reason: 'duplicate' });
  });
});

describe('connections', () => {
  it('uses descriptor port counts: IF outputs and Merge inputs', () => {
    const d = base();
    expect(checkConnection(d, types, { fromNode: 'IF', fromIndex: 1, toNode: 'Merge', toIndex: 1 })).toBeNull();
    expect(checkConnection(d, types, { fromNode: 'IF', fromIndex: 2, toNode: 'Merge', toIndex: 0 })).toBe('noOutput');
    expect(checkConnection(d, types, { fromNode: 'IF', fromIndex: 0, toNode: 'Merge', toIndex: 2 })).toBe('noInput');
    expect(checkConnection(d, types, { fromNode: 'IF', fromIndex: 0, toNode: 'Start', toIndex: 0 })).toBe('noInput');
  });
  it('refuses self, duplicate and mismatching kinds', () => {
    const d = base();
    expect(checkConnection(d, types, { fromNode: 'IF', fromIndex: 0, toNode: 'IF', toIndex: 0 })).toBe('self');
    expect(checkConnection(d, types, { fromNode: 'Start', fromIndex: 0, toNode: 'IF', toIndex: 0 })).toBe('duplicate');
    const withModel = addNode(d, { name: 'Model', type: 't.model' });
    expect(checkConnection(withModel, types, { fromNode: 'Model', fromIndex: 0, toNode: 'Merge', toIndex: 0 })).toBe('kind');
  });
  it('writes the port kind and index, and removes a single edge', () => {
    let d = addConnection(base(), types, { fromNode: 'IF', fromIndex: 1, toNode: 'Merge', toIndex: 1 });
    expect(d.connections[1]).toEqual({ from: { node: 'IF', port: 'main', index: 1 }, to: { node: 'Merge', port: 'main', index: 1 } });
    d = removeConnection(d, d.connections[1] as WorkflowDoc['connections'][number]);
    expect(d.connections).toHaveLength(1);
  });
  it('picks the first unconnected output', () => {
    const d = addConnection(base(), types, { fromNode: 'IF', fromIndex: 0, toNode: 'Merge', toIndex: 0 });
    expect(firstFreeOutput(d, types, 'IF')).toBe(1);
    expect(firstFreeOutput(d, types, 'Merge')).toBe(0);
  });
});

describe('deleting, copying, pasting', () => {
  it('removes nodes with their connections and pins', () => {
    const d = removeNodes(base(), ['IF']);
    expect(d.nodes.map((n) => n.name)).toEqual(['Start', 'Merge']);
    expect(d.connections).toEqual([]);
    expect(d.pinData).toEqual({});
  });
  it('copies a selection as JSON with only internal connections', () => {
    const f = copyFragment(base(), ['Start', 'IF']);
    expect(f.connections).toHaveLength(1);
    expect(copyFragment(base(), ['IF']).connections).toHaveLength(0);
  });
  it('pastes with unique names, remapped connections and an offset', () => {
    const d = base();
    const r = pasteFragment(d, copyFragment(d, ['Start', 'IF']));
    expect(r.names).toEqual(['Start 1', 'IF 1']);
    const edge = r.doc.connections[1];
    expect(edge?.from.node).toBe('Start 1');
    expect(edge?.to.node).toBe('IF 1');
    expect(r.doc.nodes.find((n) => n.name === 'IF 1')?.position).toEqual([240, 40]);
  });
  it('parses a pasted workflow document and normalizes connections', () => {
    const f = parseFragment(JSON.stringify({ nodes: [{ name: 'A', type: 'x' }], connections: [{ from: { node: 'A' }, to: { node: 'A' } }] }));
    expect(f?.connections[0]?.from).toEqual({ node: 'A', port: 'main', index: 0 });
    expect(parseFragment('nope')).toBeNull();
    expect(parseFragment('{"nodes":[]}')).toBeNull();
    expect(parseFragment('{"nodes":[{"x":1}]}')).toBeNull();
  });
  it('finds a free position', () => {
    expect(freePosition(base(), [0, 0])).toEqual([0, 80]);
    expect(freePosition(base(), [0, 300])).toEqual([0, 300]);
  });
});
