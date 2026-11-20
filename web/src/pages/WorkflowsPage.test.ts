import { describe, expect, it } from 'vitest';
import { blankWorkflow, parseWorkflowJson } from './WorkflowsPage';

describe('workflow import', () => {
  it('accepts an exported document and normalizes connections', () => {
    const doc = parseWorkflowJson(JSON.stringify({ name: 'x', nodes: [{ name: 'A', type: 't' }] }));
    expect(doc.connections).toEqual([]);
  });
  it('rejects text that is not JSON or not a workflow', () => {
    expect(() => parseWorkflowJson('{oops')).toThrow('not valid JSON');
    expect(() => parseWorkflowJson('{"hello":1}')).toThrow('not a workflow');
    expect(() => parseWorkflowJson('null')).toThrow('not a workflow');
  });
  it('starts a new workflow with a manual trigger', () => {
    expect(blankWorkflow('n').nodes[0]?.type).toBe('engi.trigger.manual');
  });
});
