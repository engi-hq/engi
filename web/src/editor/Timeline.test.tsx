import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { Execution, NodeRun } from '../api/types';
import { Timeline, timelineRows } from './Timeline';

const run = (over: Partial<NodeRun>): NodeRun => ({ runIndex: 0, status: 'success', ...over });
const exec = (runData: Record<string, NodeRun[]>, over: Partial<Execution> = {}): Execution => ({
  id: 'e1', workflowId: 'w1', workflowVersion: 3, mode: 'manual', status: 'success',
  startedAt: '2026-01-01T00:00:00.000Z', finishedAt: '2026-01-01T00:00:01.000Z', runData, ...over,
});

describe('timelineRows', () => {
  it('orders by start time and computes percentages of the span', () => {
    const rows = timelineRows(exec({
      b: [run({ startedAt: '2026-01-01T00:00:00.500Z', durationMs: 500, input: { main: [[{ json: {} }, { json: {} }]] }, output: { main: [[{ json: {} }], [{ json: {} }]] } })],
      a: [run({ startedAt: '2026-01-01T00:00:00.000Z', durationMs: 500 })],
    }));
    expect(rows.map((r) => r.node)).toEqual(['a', 'b']);
    expect(rows[0]).toMatchObject({ offsetPct: 0, widthPct: 50 });
    expect(rows[1]).toMatchObject({ offsetPct: 50, widthPct: 50, itemsIn: 2, itemsOut: 2, attempts: 1 });
  });
  it('keeps a minimum width for 0 ms runs', () => {
    const [r] = timelineRows(exec({ a: [run({ startedAt: '2026-01-01T00:00:00.000Z', durationMs: 0 })] }));
    expect(r?.widthPct).toBeGreaterThan(0);
  });
  it('puts runs without startedAt last at offset 0', () => {
    const rows = timelineRows(exec({ a: [run({})], b: [run({ startedAt: '2026-01-01T00:00:00.200Z', durationMs: 10 })] }));
    expect(rows.map((r) => r.node)).toEqual(['b', 'a']);
    expect(rows[1]?.offsetPct).toBe(0);
  });
  it('lists every run of a node', () => {
    const rows = timelineRows(exec({ a: [
      run({ runIndex: 0, startedAt: '2026-01-01T00:00:00.100Z', durationMs: 10 }),
      run({ runIndex: 1, startedAt: '2026-01-01T00:00:00.300Z', durationMs: 10 }),
    ] }));
    expect(rows.map((r) => [r.runIndex, r.runCount])).toEqual([[0, 2], [1, 2]]);
  });
  it('survives an empty execution', () => {
    expect(timelineRows(exec({}, { finishedAt: undefined }))).toEqual([]);
  });
});

describe('Timeline', () => {
  it('renders summary and rows, and jumps to a node on click', async () => {
    const onJump = vi.fn();
    const e = exec({ http: [run({ status: 'error', attempts: 3, startedAt: '2026-01-01T00:00:00.000Z', durationMs: 120, error: { kind: 'node', message: 'boom' } })] },
      { status: 'error', error: { kind: 'node', node: 'http', message: 'boom' } });
    render(<Timeline execution={e} onJump={onJump} onClose={() => undefined} />);
    expect(screen.getByText('e1')).toBeInTheDocument();
    expect(screen.getByText('3 attempts')).toBeInTheDocument();
    expect(screen.getByText('120 ms')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /http/ }));
    expect(onJump).toHaveBeenCalledWith('http');
  });

  it('shows the execution attempt only when the run started more than once', () => {
    const noop = () => undefined;
    const { unmount } = render(<Timeline execution={exec({}, { attempts: 1 })} onJump={noop} onClose={noop} />);
    expect(screen.queryByText(/^attempt \d+$/)).not.toBeInTheDocument();
    unmount();
    render(<Timeline execution={exec({}, { attempts: 2, lastRecoveredAt: '2026-01-01T00:00:00.500Z' })} onJump={noop} onClose={noop} />);
    expect(screen.getByText('attempt 2')).toBeInTheDocument();
  });
});
