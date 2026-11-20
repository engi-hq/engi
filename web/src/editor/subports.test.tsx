import { render, screen } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { Item, NodeType, WorkflowDoc } from '../api/types';
import { NodeCard, type CardNode } from './NodeCard';
import { addConnection, checkConnection, flatOf, indexTypes, ordinalOf, removeConnection } from './model';
import { agentInfo, hasAgentSteps, StepsView } from './StepsView';

const nt = (over: Partial<NodeType> & Pick<NodeType, 'type' | 'inputs' | 'outputs'>): NodeType => ({
  version: 1, displayName: over.type, group: 'ai', properties: [], credentials: [], ...over,
});
const agent = nt({
  type: 'engi.ai.agent', displayName: 'AI Agent',
  inputs: [
    { name: 'main', kind: 'main' },
    { name: 'model', kind: 'ai.model', displayName: 'Model', required: true, maxConnections: 1 },
    { name: 'memory', kind: 'ai.memory', displayName: 'Memory', maxConnections: 1 },
    { name: 'tools', kind: 'ai.tool', displayName: 'Tools' },
  ],
  outputs: [{ name: 'main', kind: 'main' }],
});
const model = nt({ type: 'engi.ai.model', inputs: [], outputs: [{ name: 'model', kind: 'ai.model' }] });
const tool = nt({ type: 'engi.ai.tool.http', inputs: [], outputs: [{ name: 'tool', kind: 'ai.tool' }] });
const trigger = nt({ type: 'engi.trigger.manual', group: 'trigger', inputs: [], outputs: [{ name: 'main', kind: 'main' }] });
const types = indexTypes([agent, model, tool, trigger]);

const doc = (): WorkflowDoc => ({
  name: 'wf',
  nodes: [
    { name: 'Start', type: trigger.type }, { name: 'Agent', type: agent.type },
    { name: 'M1', type: model.type }, { name: 'M2', type: model.type }, { name: 'T1', type: tool.type }, { name: 'T2', type: tool.type },
  ],
  connections: [],
});

describe('sub-port connections', () => {
  it('maps flat port positions to ordinals among their kind', () => {
    expect(ordinalOf(agent.inputs, 1)).toBe(0);
    expect(ordinalOf(agent.inputs, 3)).toBe(0);
    expect(flatOf(agent.inputs, 'ai.tool', 0)).toBe(3);
    expect(flatOf(agent.inputs, 'ai.tool', 1)).toBe(-1);
  });
  it('stores the ordinal and the kind, and allows matching kinds only', () => {
    const d = doc();
    expect(checkConnection(d, types, { fromNode: 'M1', fromIndex: 0, toNode: 'Agent', toIndex: 1 })).toBeNull();
    expect(checkConnection(d, types, { fromNode: 'M1', fromIndex: 0, toNode: 'Agent', toIndex: 3 })).toBe('kind');
    expect(checkConnection(d, types, { fromNode: 'M1', fromIndex: 0, toNode: 'Agent', toIndex: 0 })).toBe('kind');
    expect(checkConnection(d, types, { fromNode: 'Start', fromIndex: 0, toNode: 'Agent', toIndex: 1 })).toBe('kind');
    const next = addConnection(d, types, { fromNode: 'T1', fromIndex: 0, toNode: 'Agent', toIndex: 3 });
    expect(next.connections[0]).toEqual({ from: { node: 'T1', port: 'ai.tool', index: 0 }, to: { node: 'Agent', port: 'ai.tool', index: 0 } });
  });
  it('enforces maxConnections but not on unlimited inputs', () => {
    let d = addConnection(doc(), types, { fromNode: 'M1', fromIndex: 0, toNode: 'Agent', toIndex: 1 });
    expect(checkConnection(d, types, { fromNode: 'M2', fromIndex: 0, toNode: 'Agent', toIndex: 1 })).toBe('maxConnections');
    expect(checkConnection(d, types, { fromNode: 'M1', fromIndex: 0, toNode: 'Agent', toIndex: 1 })).toBe('duplicate');
    d = addConnection(d, types, { fromNode: 'T1', fromIndex: 0, toNode: 'Agent', toIndex: 3 });
    expect(checkConnection(d, types, { fromNode: 'T2', fromIndex: 0, toNode: 'Agent', toIndex: 3 })).toBeNull();
    d = removeConnection(d, d.connections[0] as WorkflowDoc['connections'][number]);
    expect(checkConnection(d, types, { fromNode: 'M2', fromIndex: 0, toNode: 'Agent', toIndex: 1 })).toBeNull();
  });
  it('a main connection keeps working next to sub-ports', () => {
    const d = addConnection(doc(), types, { fromNode: 'Start', fromIndex: 0, toNode: 'Agent', toIndex: 0 });
    expect(d.connections[0]?.to).toEqual({ node: 'Agent', port: 'main', index: 0 });
  });
});

function card(nodeType: NodeType, name: string) {
  const data: CardNode['data'] = { node: { name, type: nodeType.type }, nt: nodeType };
  const props = { id: name, data, selected: false } as unknown as Parameters<typeof NodeCard>[0];
  return render(<ReactFlowProvider><NodeCard {...props} /></ReactFlowProvider>);
}

describe('node card sub-ports', () => {
  it('labels sub-port inputs by kind at the bottom and marks the required one', () => {
    const { container } = card(agent, 'Agent');
    const labels = [...container.querySelectorAll('.sub-label')].map((e) => [e.getAttribute('data-kind'), e.textContent]);
    expect(labels).toEqual([['ai.model', 'Model*'], ['ai.memory', 'Memory'], ['ai.tool', 'Tools']]);
    expect(screen.getByLabelText('Model input (connect a ai.model node)')).toBeInTheDocument();
    expect(screen.getByLabelText('Input main')).toBeInTheDocument();
  });
  it('draws a sub-node output on top and no main ports', () => {
    const { container } = card(tool, 'T1');
    expect(screen.getByLabelText('tool output (ai.tool)')).toBeInTheDocument();
    expect(container.querySelector('.react-flow__handle-top')).not.toBeNull();
    expect(container.querySelector('.port.main')).toBeNull();
    expect(container.querySelector('.ncard.subnode')).not.toBeNull();
  });
});

const agentItem = (steps: unknown): Item => ({ json: { output: 'ok', iterations: 2, stopReason: 'end', usage: { inputTokens: 5, outputTokens: 7 }, steps } });

describe('StepsView', () => {
  it('reads agent info and ignores other items', () => {
    expect(agentInfo({ json: { a: 1 } })).toBeNull();
    expect(agentInfo({ json: { steps: [{ nope: 1 }] } })).toBeNull();
    expect(hasAgentSteps([{ json: {} }, agentItem([])])).toBe(true);
  });
  it('lists tool calls with input, output, errors and durations', async () => {
    const items = [agentItem([
      { tool: 'weather', input: { city: 'Oslo' }, output: { temp: 4 }, isError: false, durationMs: 12 },
      { tool: 'flaky', input: {}, output: 'boom', isError: true, durationMs: 3 },
    ])];
    render(<StepsView items={items} />);
    expect(screen.getByText('2 tool calls')).toBeInTheDocument();
    expect(screen.getByText('2 model calls')).toBeInTheDocument();
    expect(screen.getByText('Tokens: 5 in, 7 out')).toBeInTheDocument();
    expect(screen.getByText('weather')).toBeInTheDocument();
    expect(screen.getByText('12 ms')).toBeInTheDocument();
    expect(screen.getByText('failed')).toBeInTheDocument();
    await userEvent.click(screen.getByText('weather'));
    expect(screen.getAllByRole('tree').length).toBeGreaterThan(0);
  });
  it('flags a limit stop and the no-tool case', () => {
    render(<StepsView items={[{ json: { ...agentItem([]).json, stopReason: 'maxIterations' } }]} />);
    expect(screen.getByText('Stopped: maxIterations')).toBeInTheDocument();
    expect(screen.getByText('The agent answered without calling a tool.')).toBeInTheDocument();
  });
  it('renders nothing for non-agent output', () => {
    const { container } = render(<StepsView items={[{ json: { a: 1 } }]} />);
    expect(container).toBeEmptyDOMElement();
  });
});
