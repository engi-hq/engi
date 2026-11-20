import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { WfNode } from '../api/types';
import { draftOf, hasErrorTrigger, settingsFromDraft, WorkflowSettingsDialog } from './WorkflowSettings';

const json = (status: number, body: unknown) =>
  Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }));

const node = (type: string, disabled = false): WfNode => ({ name: type, type, typeVersion: 1, position: [0, 0], disabled }) as WfNode;
const list = { data: [{ id: 'w2', name: 'Alert Slack' }, { id: 'w3', name: 'Page ops' }, { id: 'w1', name: 'Self' }], hasMore: false };
const detail = (id: string, types: string[]) => ({ id, name: id, version: 1, nodes: types.map((x) => node(x)), connections: [] });

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => { fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock); });
afterEach(() => vi.unstubAllGlobals());

function open(props: Partial<React.ComponentProps<typeof WorkflowSettingsDialog>> = {}) {
  const onApply = vi.fn();
  const onClose = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <WorkflowSettingsDialog workflowId="w1" nodes={[node('engi.manual')]} settings={undefined} readOnly={false} onApply={onApply} onClose={onClose} {...props} />
    </QueryClientProvider>,
  );
  return { onApply, onClose };
}

describe('settings helpers', () => {
  it('round-trips and keeps unknown keys', () => {
    const base = { errorWorkflowId: 'w2', timeoutSeconds: 30, future: true };
    expect(draftOf(base)).toEqual({ errorWorkflowId: 'w2', timeoutSeconds: 30 });
    expect(settingsFromDraft(base, { errorWorkflowId: '', timeoutSeconds: 0 })).toEqual({ future: true });
  });
  it('ignores disabled error triggers', () => {
    expect(hasErrorTrigger([node('engi.trigger.error', true)])).toBe(false);
    expect(hasErrorTrigger([node('engi.trigger.error')])).toBe(true);
  });
});

describe('WorkflowSettingsDialog', () => {
  it('loads workflows (without the current one) and applies the chosen error workflow', async () => {
    fetchMock.mockImplementation((url: string) => String(url).includes('/workflows/w2') ? json(200, detail('w2', ['engi.trigger.error'])) : json(200, list));
    const { onApply } = open({ settings: { timeoutSeconds: 20 } });
    await userEvent.click(await screen.findByRole('button', { name: 'Alert Slack' }));
    expect(screen.queryByRole('button', { name: 'Self' })).not.toBeInTheDocument();
    await waitFor(() => expect(fetchMock.mock.calls.some((c) => String(c[0]).includes('/workflows/w2'))).toBe(true));
    expect(screen.queryByText(/no enabled Error Trigger/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onApply).toHaveBeenCalledWith({ timeoutSeconds: 20, errorWorkflowId: 'w2' });
  });

  it('warns, without blocking, when the chosen workflow has no Error Trigger', async () => {
    fetchMock.mockImplementation((url: string) => String(url).includes('/workflows/w3') ? json(200, detail('w3', ['engi.manual'])) : json(200, list));
    const { onApply } = open();
    await userEvent.click(await screen.findByRole('button', { name: 'Page ops' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('"Page ops" has no enabled Error Trigger node');
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onApply).toHaveBeenCalledWith({ errorWorkflowId: 'w3' });
  });

  it('supports "This workflow" and checks the open document for a trigger', async () => {
    fetchMock.mockImplementation(() => json(200, list));
    const { onApply } = open();
    await userEvent.click(await screen.findByRole('button', { name: 'This workflow' }));
    expect(screen.getByRole('alert')).toHaveTextContent('This workflow has no enabled Error Trigger node');
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onApply).toHaveBeenCalledWith({ errorWorkflowId: 'w1' });
  });

  it('this workflow with an Error Trigger shows no warning', async () => {
    fetchMock.mockImplementation(() => json(200, list));
    open({ nodes: [node('engi.trigger.error')], settings: { errorWorkflowId: 'w1' } });
    await screen.findByRole('button', { name: 'Alert Slack' });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'This workflow' })).toHaveAttribute('aria-selected', 'true');
  });

  it('None clears the setting', async () => {
    fetchMock.mockImplementation((url: string) => String(url).includes('/workflows/w2') ? json(200, detail('w2', ['engi.trigger.error'])) : json(200, list));
    const { onApply } = open({ settings: { errorWorkflowId: 'w2' } });
    await userEvent.click(await screen.findByRole('button', { name: 'None' }));
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onApply).toHaveBeenCalledWith({});
  });

  it('searches through the API', async () => {
    fetchMock.mockImplementation(() => json(200, list));
    open();
    await screen.findByRole('button', { name: 'Page ops' });
    await userEvent.type(screen.getByLabelText('Search workflows'), 'ops');
    await waitFor(() => expect(fetchMock.mock.calls.some((c) => String(c[0]).includes('q=ops'))).toBe(true));
  });
});
