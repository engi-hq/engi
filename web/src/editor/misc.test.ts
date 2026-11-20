import { describe, expect, it } from 'vitest';
import type { Execution, NodeType } from '../api/types';
import { diffDocs } from './ConflictDialog';
import { searchNodeTypes, fuzzyScore } from './paletteSearch';
import { applyStreamEvent, statesFromExecution } from './runState';
import { settingsToValues, valuesToSettings } from './settings';
import { previewScope, visibleSlots } from './NodePanel';

const nt = (over: Partial<NodeType>): NodeType => ({ type: 'x.a', version: 1, displayName: 'A', group: 'action', inputs: [], outputs: [], properties: [], credentials: [], ...over });

describe('palette search', () => {
  const list = [
    nt({ type: 'engi.http.request', displayName: 'HTTP Request', description: 'Call an API' }),
    nt({ type: 'engi.transform.set', displayName: 'Edit Fields (Set)', group: 'transform' }),
    nt({ type: 'engi.flow.if', displayName: 'IF', group: 'flow', description: 'Route items' }),
  ];
  it('ranks prefix matches first and fuzzy-matches subsequences', () => {
    expect(searchNodeTypes(list, 'http')[0]?.type).toBe('engi.http.request');
    expect(searchNodeTypes(list, 'hrq').map((n) => n.type)).toContain('engi.http.request');
    expect(searchNodeTypes(list, 'zzz')).toEqual([]);
  });
  it('matches the group and the description', () => {
    expect(searchNodeTypes(list, 'transform').map((n) => n.type)).toContain('engi.transform.set');
    expect(searchNodeTypes(list, 'route').map((n) => n.type)).toEqual(['engi.flow.if']);
  });
  it('puts recent nodes first for an empty query', () => {
    expect(searchNodeTypes(list, '', ['engi.flow.if'])[0]?.type).toBe('engi.flow.if');
    expect(fuzzyScore('', 'anything')).toBeGreaterThan(0);
  });
});

describe('run state', () => {
  const ex: Execution = {
    id: 'ex_1', workflowId: 'wf_1', workflowVersion: 1, mode: 'manual', status: 'error', startedAt: 'x',
    runData: {
      A: [{ runIndex: 0, status: 'success', durationMs: 3, output: { main: [[{ json: {} }, { json: {} }], []] } }],
      B: [{ runIndex: 0, status: 'success' }, { runIndex: 1, status: 'error', error: { kind: 'node', message: 'boom' } }],
    },
  };
  it('derives node states from the last run', () => {
    const s = statesFromExecution(ex);
    expect(s['A']).toMatchObject({ status: 'success', outputItems: [2, 0] });
    expect(s['B']).toMatchObject({ status: 'error', error: 'boom' });
  });
  it('folds stream events', () => {
    let s = applyStreamEvent({}, 'node.started', { executionId: 'e', at: '', node: 'A' });
    expect(s['A']?.status).toBe('running');
    s = applyStreamEvent(s, 'node.finished', { executionId: 'e', at: '', node: 'A', nodeStatus: 'success', outputItems: [3] });
    expect(s['A']).toMatchObject({ status: 'success', outputItems: [3] });
    expect(applyStreamEvent(s, 'execution.finished', { executionId: 'e', at: '' })).toBe(s);
  });
  it('builds the expression preview scope from run data', () => {
    const e2: Execution = { ...ex, runData: { Up: [{ runIndex: 0, status: 'success', output: { main: [[{ json: { id: 1 } }]] } }], Me: [{ runIndex: 0, status: 'success', input: { main: [[{ json: { id: 1 } }]] } }] } };
    const sc = previewScope(e2, 'Me');
    expect(sc.json).toEqual({ id: 1 });
    expect(sc.nodes?.['Up']).toEqual({ id: 1 });
  });
});

describe('node settings mapping', () => {
  it('round-trips retry, onError and flags', () => {
    const wire = { retry: { maxAttempts: 3, backoff: 'exponential', initialDelayMs: 500 }, onError: 'continue' as const, executeOnce: true, timeoutMs: 1000 };
    const { settings, notes } = valuesToSettings(settingsToValues(wire, 'note'));
    expect(settings).toEqual(wire);
    expect(notes).toBe('note');
  });
  it('writes nothing for an untouched form', () => {
    expect(valuesToSettings({}).settings).toEqual({});
  });
});

describe('credential slots', () => {
  const type = nt({
    properties: [
      { name: 'authentication', type: 'options', options: [{ name: 'None', value: 'none' }, { name: 'Predefined', value: 'predefined' }, { name: 'Generic', value: 'generic' }], default: 'none' },
      { name: 'genericAuthType', type: 'options', options: [{ name: 'Basic', value: 'engi.http.basic' }], default: 'engi.http.basic', displayOptions: { show: { authentication: ['generic'] } } },
    ],
    credentials: [{ name: 'predefined', type: '' }, { name: 'engi.http.basic', type: 'engi.http.basic' }, { name: 'other', type: 'engi.other' }],
  });
  it('shows a slot only when its selecting option is chosen', () => {
    expect(visibleSlots(type, {}).map((s) => s.name)).toEqual(['other']);
    expect(visibleSlots(type, { authentication: 'predefined' }).map((s) => s.name)).toEqual(['predefined', 'other']);
    expect(visibleSlots(type, { authentication: 'generic' }).map((s) => s.name)).toEqual(['engi.http.basic', 'other']);
  });
});

describe('conflict diff', () => {
  it('lists added, removed and changed nodes', () => {
    const a = { name: 'w', nodes: [{ name: 'A', type: 't' }, { name: 'B', type: 't', disabled: true }, { name: 'C', type: 't' }], connections: [] };
    const b = { name: 'w', nodes: [{ name: 'A', type: 't' }, { name: 'B', type: 't' }, { name: 'D', type: 't' }], connections: [] };
    expect(diffDocs(a, b)).toEqual({
      added: ['C'], removed: ['D'], changed: ['B'], connectionsChanged: false, notesAdded: [], notesRemoved: [], notesChanged: [],
    });
  });

  it('lists added, removed and changed sticky notes by first line', () => {
    const note = (id: string, text: string, x = 0) => ({ id, text, position: [x, 0] as [number, number], size: { width: 240, height: 160 } });
    const base = { name: 'w', nodes: [], connections: [] };
    const mine = { ...base, notes: [note('a', 'Keep\nmore'), note('b', 'Moved', 5), note('new', '')] };
    const theirs = { ...base, notes: [note('b', 'Moved', 9), note('gone', 'Gone soon')] };
    expect(diffDocs(mine, theirs)).toMatchObject({
      notesAdded: ['Keep', 'new'], notesRemoved: ['Gone soon'], notesChanged: ['Moved'],
    });
    expect(diffDocs(mine, mine)).toMatchObject({ notesAdded: [], notesRemoved: [], notesChanged: [] });
  });
});
