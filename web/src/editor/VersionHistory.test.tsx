import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HistoryPanel, VersionBanner } from './VersionHistory';

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => { fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock); });
afterEach(() => vi.unstubAllGlobals());

const wrap = (ui: React.ReactNode) =>
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider>);

describe('HistoryPanel', () => {
  it('lists versions, pages by cursor and reports selection', async () => {
    fetchMock
      .mockImplementationOnce(() => json(200, { data: [{ version: 3, name: 'w', createdAt: '2026-01-03T00:00:00Z' }, { version: 2, name: 'w', createdAt: '2026-01-02T00:00:00Z' }], hasMore: true, nextCursor: 'c1' }))
      .mockImplementationOnce(() => json(200, { data: [{ version: 1, name: 'w', createdAt: '2026-01-01T00:00:00Z' }], hasMore: false }));
    const onSelect = vi.fn();
    wrap(<HistoryPanel workflowId="w1" selected={null} current={3} onSelect={onSelect} onClose={() => {}} />);
    expect(await screen.findByRole('button', { name: /Version 3/ })).toBeInTheDocument();
    expect(screen.getByText('current')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByRole('button', { name: /Version 1/ })).toBeInTheDocument();
    expect(String(fetchMock.mock.calls[1]?.[0])).toContain('cursor=c1');
    await userEvent.click(screen.getByRole('button', { name: /Version 2/ }));
    expect(onSelect).toHaveBeenCalledWith(2);
  });
});

describe('VersionBanner', () => {
  const doc = { id: 'w1', name: 'w', version: 4, nodes: [], connections: [] };

  it('announces read-only view and restores without confirmation when clean', async () => {
    fetchMock.mockImplementationOnce(() => json(200, doc));
    const onRestored = vi.fn();
    render(<VersionBanner workflowId="w1" version={2} dirty={false} onBack={() => {}} onRestored={onRestored} />);
    expect(screen.getByRole('status')).toHaveTextContent('Viewing version 2 (read-only)');
    await userEvent.click(screen.getByRole('button', { name: 'Restore this version' }));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/workflows/w1/versions/2/restore');
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' });
  });

  it('asks for confirmation when there are unsaved changes', async () => {
    fetchMock.mockImplementationOnce(() => json(200, doc));
    const onRestored = vi.fn();
    render(<VersionBanner workflowId="w1" version={2} dirty onBack={() => {}} onRestored={onRestored} />);
    await userEvent.click(screen.getByRole('button', { name: 'Restore this version' }));
    expect(fetchMock).not.toHaveBeenCalled();
    const dlg = screen.getByRole('dialog');
    await userEvent.click(within(dlg).getByRole('button', { name: 'Restore this version' }));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
  });

  it('shows a conflict message on 409 and a generic error otherwise', async () => {
    fetchMock.mockImplementationOnce(() => json(409, { title: 'Conflict', status: 409 }));
    const onRestored = vi.fn();
    render(<VersionBanner workflowId="w1" version={2} dirty={false} onBack={() => {}} onRestored={onRestored} />);
    await userEvent.click(screen.getByRole('button', { name: 'Restore this version' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('changed while you were viewing');
    fetchMock.mockImplementationOnce(() => json(500, { title: 'Boom', status: 500 }));
    await userEvent.click(screen.getByRole('button', { name: 'Restore this version' }));
    await waitFor(() => expect(screen.getByRole('alert')).not.toHaveTextContent('changed while'));
    expect(onRestored).not.toHaveBeenCalled();
  });

  it('calls onBack', async () => {
    const onBack = vi.fn();
    render(<VersionBanner workflowId="w1" version={2} dirty={false} onBack={onBack} onRestored={() => {}} />);
    await userEvent.click(screen.getByRole('button', { name: 'Back to current' }));
    expect(onBack).toHaveBeenCalled();
  });
});
