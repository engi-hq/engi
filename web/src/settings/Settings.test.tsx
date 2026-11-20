import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AuthProvider } from '../auth/AuthContext';
import { ToastProvider } from '../ui/Toast';
import { ApiKeysPage } from './ApiKeysPage';
import { SettingsLayout } from './SettingsLayout';
import { UsersPage } from './UsersPage';

const mk = (role: string) => ({ id: 'us_1', email: 'o@x.co', name: 'Olly', role, disabled: false, createdAt: '', updatedAt: '' });
const reply = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' } });

interface Opts { role: string; keys?: unknown[]; createUser?: () => Response }
function mock(o: Opts) {
  const calls: Array<{ url: string; method: string }> = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET';
    calls.push({ url, method });
    if (url.endsWith('/me')) return reply({ user: mk(o.role), authKind: 'session', csrfToken: 'c' });
    if (url.includes('/users') && method === 'POST') return o.createUser?.() ?? reply(mk('member'), 201);
    if (url.includes('/users')) return reply({ data: [mk('owner')], hasMore: false });
    if (url.includes('/api-keys') && method === 'POST') {
      return reply({ id: 'k2', name: 'ci', prefix: 'engi_ab', scopes: ['read'], createdAt: '2026-01-01T00:00:00Z', token: 'engi_SECRET_TOKEN' }, 201);
    }
    if (url.includes('/api-keys') && method === 'DELETE') return new Response(null, { status: 204 });
    if (url.includes('/api-keys')) return reply({ data: o.keys ?? [], hasMore: false });
    return reply({}, 404);
  }));
  return calls;
}

function renderApp(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route path="/settings" element={<SettingsLayout />}>
                <Route path="users" element={<UsersPage />} />
                <Route path="api-keys" element={<ApiKeysPage />} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe('settings', () => {
  it('shows the Users link to owners', async () => {
    mock({ role: 'owner' });
    renderApp('/settings/api-keys');
    expect(await screen.findByRole('link', { name: 'Users' })).toBeInTheDocument();
  });

  it('hides Users from members and does not list users', async () => {
    const calls = mock({ role: 'member' });
    renderApp('/settings/users');
    expect(await screen.findByText('Only the owner can manage users.')).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull();
    expect(calls.some((c) => c.url.includes('/users'))).toBe(false);
  });

  it('shows the API key secret once', async () => {
    mock({ role: 'owner' });
    renderApp('/settings/api-keys');
    await userEvent.click(await screen.findByRole('button', { name: /New API key/ }));
    await userEvent.type(screen.getByLabelText('Name'), 'ci');
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'New API key' }));
    const secret = await screen.findByDisplayValue('engi_SECRET_TOKEN');
    expect(secret).toBeInTheDocument();
    expect(screen.getByText(/will not see it again/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'I have copied it' }));
    expect(screen.queryByDisplayValue('engi_SECRET_TOKEN')).toBeNull();
  });

  it('confirms before revoking a key', async () => {
    const key = { id: 'k1', name: 'deploy', prefix: 'engi_zz', scopes: ['write'], createdAt: '2026-01-01T00:00:00Z' };
    const calls = mock({ role: 'owner', keys: [key] });
    renderApp('/settings/api-keys');
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke deploy' }));
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/api-keys/k1'))).toBe(true));
  });

  it('shows problem+json field errors when creating a user', async () => {
    mock({
      role: 'owner',
      createUser: () => reply({ title: 'Unprocessable', status: 422, errors: [{ pointer: '/password', message: 'too short' }] }, 422),
    });
    renderApp('/settings/users');
    await userEvent.click(await screen.findByRole('button', { name: /Add user/ }));
    const d = within(screen.getByRole('dialog'));
    await userEvent.type(d.getByLabelText('Name'), 'Mia');
    await userEvent.type(d.getByLabelText('Email'), 'm@x.co');
    await userEvent.type(d.getByLabelText(/^Password/), 'short');
    await userEvent.click(d.getByRole('button', { name: 'Create user' }));
    expect(await d.findByText('too short')).toBeInTheDocument();
  });
});
