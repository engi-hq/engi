import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { WorkflowDoc } from '../api/types';
import { useRunner } from './useRunner';

const post = vi.fn();
vi.mock('../api/client', async (orig) => ({ ...(await orig<Record<string, unknown>>()), api: { post: (...a: unknown[]) => post(...a), get: vi.fn() } }));
vi.mock('../api/sse', () => ({ subscribeExecution: () => ({ close: vi.fn() }) }));

describe('useRunner partial runs', () => {
  beforeEach(() => post.mockReset().mockResolvedValue({ executionId: 'e1' }));
  it('sends destination, the unsaved workflow and pinData', async () => {
    const wf: WorkflowDoc = { name: 'w', nodes: [], connections: [], pinData: { A: [{ x: 1 }] } };
    const { result } = renderHook(() => useRunner('wf1'));
    await act(async () => { await result.current.run({ destination: { node: 'B', mode: 'exclusive' }, workflow: wf, pinData: wf.pinData }); });
    expect(post).toHaveBeenCalledWith('/workflows/wf1/run', { destination: { node: 'B', mode: 'exclusive' }, workflow: wf, pinData: { A: [{ x: 1 }] } });
    expect(result.current.state.executionId).toBe('e1');
  });
});
