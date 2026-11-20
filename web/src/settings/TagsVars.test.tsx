import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CompletionContext } from '@codemirror/autocomplete';
import { EditorState } from '@codemirror/state';
import { useState } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { expressionCompletions } from '../forms/CodeEditor';
import { TagEditor } from '../editor/TagEditor';
import { WorkflowsPage } from '../pages/WorkflowsPage';
import { ToastProvider } from '../ui/Toast';
import { TagsPage } from './TagsPage';
import { VariablesPage } from './VariablesPage';

const reply = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' } });
const page = (data: unknown[]) => reply({ data, hasMore: false });

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><ToastProvider><MemoryRouter>{ui}</MemoryRouter></ToastProvider></QueryClientProvider>);
}
afterEach(() => vi.unstubAllGlobals());

describe('tags', () => {
  it('filters the workflow list by tag', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      if (url.includes('/tags')) return page([{ id: 'tg_1', name: 'prod' }]);
      urls.push(url);
      return page([{ id: 'wf_1', name: 'Flow', active: false, version: 1, tags: ['prod'], nodeCount: 1 }]);
    }));
    wrap(<WorkflowsPage />);
    await screen.findByText('Flow');
    expect(screen.getAllByText('prod').length).toBeGreaterThan(0);
    await userEvent.click(await screen.findByRole('button', { name: 'prod' }));
    await waitFor(() => expect(urls.some((u) => u.includes('tag=prod'))).toBe(true));
  });

  it('adding and removing a tag reports changes (marks the workflow dirty)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => page([{ id: 'tg_1', name: 'prod' }])));
    const seen: string[][] = [];
    function Host() {
      const [tags, setTags] = useState<string[]>(['a']);
      return <TagEditor tags={tags} readOnly={false} onChange={(n) => { seen.push(n); setTags(n); }} />;
    }
    wrap(<Host />);
    await userEvent.type(screen.getByLabelText('Add tag'), 'new{Enter}');
    expect(seen.at(-1)).toEqual(['a', 'new']);
    await userEvent.click(screen.getByRole('button', { name: 'Remove tag a' }));
    expect(seen.at(-1)).toEqual(['new']);
  });

  it('renames and deletes a tag', async () => {
    const calls: Array<{ url: string; method: string; body?: string }> = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
      const method = init.method ?? 'GET';
      calls.push({ url, method, body: init.body as string | undefined });
      if (method === 'PATCH') return reply({ id: 'tg_1', name: 'live' });
      if (method === 'DELETE') return new Response(null, { status: 204 });
      return page([{ id: 'tg_1', name: 'prod' }]);
    }));
    wrap(<TagsPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Rename tag prod' }));
    const input = screen.getByDisplayValue('prod');
    await userEvent.clear(input);
    await userEvent.type(input, 'live');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(calls.find((c) => c.method === 'PATCH')?.body).toBe('{"name":"live"}'));
    await userEvent.click(await screen.findByRole('button', { name: 'Delete tag prod' }));
    await userEvent.click(screen.getAllByRole('button', { name: 'Delete' }).at(-1) as HTMLElement);
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/tags/tg_1'))).toBe(true));
  });
});

describe('variables', () => {
  it('shows 409 and 422 errors on create', async () => {
    let status = 409;
    vi.stubGlobal('fetch', vi.fn(async (_u: string, init: RequestInit = {}) => {
      if (init.method === 'POST') {
        return status === 409
          ? reply({ title: 'Conflict', status: 409 }, 409)
          : reply({ title: 'Invalid', status: 422, errors: [{ pointer: '/key', message: 'bad key shape' }] }, 422);
      }
      return page([]);
    }));
    wrap(<VariablesPage />);
    await userEvent.click(await screen.findByRole('button', { name: /New variable/ }));
    await userEvent.type(screen.getByLabelText(/^Key/), 'API_URL');
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('A variable with this key already exists.')).toBeTruthy();
    status = 422;
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('bad key shape')).toBeTruthy();
  });

  it('keeps the secret sentinel on edit and requires a new value to turn secret off', async () => {
    const bodies: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_u: string, init: RequestInit = {}) => {
      if (init.method === 'PATCH') { bodies.push(init.body as string); return reply({ id: 'vr_1', key: 'TOKEN', value: '__ENGI_SECRET__', secret: true }); }
      return page([{ id: 'vr_1', key: 'TOKEN', value: '__ENGI_SECRET__', secret: true }]);
    }));
    wrap(<VariablesPage />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit variable TOKEN' }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(JSON.parse(bodies[0] as string)).toEqual({ key: 'TOKEN', value: '__ENGI_SECRET__', secret: true });

    await userEvent.click(await screen.findByRole('button', { name: 'Edit variable TOKEN' }));
    await userEvent.click(screen.getByRole('switch', { name: 'Secret' }));
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Enter a new value to turn off secret.')).toBeTruthy();
    expect(bodies).toHaveLength(1);
  });
});

describe('$vars autocomplete', () => {
  it('offers variable keys after $vars.', async () => {
    const doc = '{{ $vars.A';
    const ctx = new CompletionContext(EditorState.create({ doc }), doc.length, true);
    const res = await expressionCompletions([], [], ['API_URL', 'TOKEN'])(ctx);
    expect(res?.options.map((o) => o.label)).toEqual(['API_URL', 'TOKEN']);
  });
});
