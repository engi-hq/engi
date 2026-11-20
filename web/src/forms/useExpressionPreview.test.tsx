import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../api/client';
import { useExpressionPreview } from './useExpressionPreview';

const remote = vi.fn();
vi.mock('../api/hooks', () => ({ previewExpressionRemote: (...a: unknown[]) => remote(...a) }));

const target = { node: 'N', workflowId: 'w1' };

describe('useExpressionPreview', () => {
  beforeEach(() => { vi.useFakeTimers(); remote.mockReset(); });
  afterEach(() => vi.useRealTimers());

  it('debounces and aborts the previous request', async () => {
    const signals: AbortSignal[] = [];
    remote.mockImplementation((_b, s: AbortSignal) => { signals.push(s); return new Promise(() => {}); });
    const { rerender } = renderHook(({ e }) => useExpressionPreview(e, target, 100), { initialProps: { e: '={{ 1 }}' } });
    remote.mockClear();
    rerender({ e: '={{ 12 }}' });
    rerender({ e: '={{ 123 }}' });
    expect(remote).not.toHaveBeenCalled();
    await act(async () => { vi.advanceTimersByTime(100); });
    expect(remote).toHaveBeenCalledTimes(1);
    expect(remote.mock.calls[0]![0]).toMatchObject({ expression: '={{ 123 }}', node: 'N', workflowId: 'w1' });
    rerender({ e: '={{ 1234 }}' });
    await act(async () => { vi.advanceTimersByTime(100); });
    expect(signals[1]!.aborted).toBe(true);
    expect(remote).toHaveBeenCalledTimes(2);
  });

  it('returns the value, the expression error, or unavailable', async () => {
    remote.mockResolvedValueOnce({ value: 42 });
    const { result, rerender } = renderHook(({ e }) => useExpressionPreview(e, target, 10), { initialProps: { e: '={{ a }}' } });
    await act(async () => { vi.advanceTimersByTime(10); });
    expect(result.current).toEqual({ state: 'value', value: '42' });

    remote.mockRejectedValueOnce(new ApiError({ title: 'Bad', status: 422, type: 'urn:engi:expression-error', detail: 'boom at 1:2' }));
    rerender({ e: '={{ b }}' });
    await act(async () => { vi.advanceTimersByTime(10); });
    expect(result.current).toEqual({ state: 'error', message: 'boom at 1:2' });

    remote.mockRejectedValueOnce(new ApiError({ title: 'Not found', status: 404 }));
    rerender({ e: '={{ c }}' });
    await act(async () => { vi.advanceTimersByTime(10); });
    expect(result.current.state).toBe('unavailable');
  });

  it('does nothing without a target', () => {
    const { result } = renderHook(() => useExpressionPreview('={{ 1 }}', undefined, 10));
    expect(result.current.state).toBe('idle');
    expect(remote).not.toHaveBeenCalled();
  });
});
