import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setCsrfToken } from '../api/client';
import { ToastProvider } from '../ui/Toast';
import { AuthProvider } from './AuthContext';
import { LoginPage, RequireAuth, SetupPage } from './AuthPages';

const user = { id: 'us_1', email: 'a@b.co', name: 'A', role: 'owner', disabled: false, createdAt: '', updatedAt: '' };
const reply = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' } });

function mockApi(opts: { setupRequired: boolean; loggedIn: boolean }) {
  let loggedIn = opts.loggedIn;
  const calls: Array<{ url: string; method: string; headers: Record<string, string>; body?: string }> = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, method: init.method ?? 'GET', headers: (init.headers ?? {}) as Record<string, string>, body: init.body as string | undefined });
    if (url.endsWith('/auth/setup') && init.method !== 'POST') return reply({ required: opts.setupRequired });
    if (url.endsWith('/auth/setup')) return reply(user, 201);
    if (url.endsWith('/auth/login')) {
      const b = JSON.parse(init.body as string) as { password: string };
      if (b.password !== 'right-password') return reply({ title: 'Unauthorized', status: 401, detail: 'invalid email or password' }, 401);
      loggedIn = true;
      return reply({ user, csrfToken: 'csrf-1', expiresAt: '' });
    }
    if (url.endsWith('/me')) return loggedIn ? reply({ user, authKind: 'session', csrfToken: 'csrf-1' }) : reply({ title: 'Unauthorized', status: 401 }, 401);
    return reply({}, 404);
  }));
  return calls;
}

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/setup" element={<SetupPage />} />
              <Route path="/workflows" element={<RequireAuth><h1>Workflows page</h1></RequireAuth>} />
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => setCsrfToken(undefined));
afterEach(() => vi.unstubAllGlobals());

describe('auth screens', () => {
  it('redirects a visitor to the login page and shows an error for a wrong password', async () => {
    mockApi({ setupRequired: false, loggedIn: false });
    renderAt('/workflows');
    await screen.findByRole('heading', { name: 'Sign in' });
    await userEvent.type(screen.getByLabelText('Email'), 'a@b.co');
    await userEvent.type(screen.getByLabelText('Password'), 'nope');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid email or password');
  });

  it('logs in and returns to the page that was asked for', async () => {
    mockApi({ setupRequired: false, loggedIn: false });
    renderAt('/workflows');
    await screen.findByRole('heading', { name: 'Sign in' });
    await userEvent.type(screen.getByLabelText('Email'), 'a@b.co');
    await userEvent.type(screen.getByLabelText('Password'), 'right-password');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Workflows page' })).toBeInTheDocument();
  });

  it('sends the login page to first-run setup when no owner exists', async () => {
    mockApi({ setupRequired: true, loggedIn: false });
    renderAt('/login');
    expect(await screen.findByRole('heading', { name: 'Set up Engi' })).toBeInTheDocument();
  });

  it('creates the owner, then signs in with the CSRF token it received', async () => {
    const calls = mockApi({ setupRequired: true, loggedIn: false });
    renderAt('/setup');
    await screen.findByRole('heading', { name: 'Set up Engi' });
    await userEvent.type(screen.getByLabelText('Your name'), 'A');
    await userEvent.type(screen.getByLabelText('Email'), 'a@b.co');
    await userEvent.type(screen.getByLabelText(/^Password/), 'right-password');
    await userEvent.type(screen.getByLabelText('Confirm password'), 'other-password');
    await userEvent.click(screen.getByRole('button', { name: 'Create owner account' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('do not match');
    await userEvent.clear(screen.getByLabelText('Confirm password'));
    await userEvent.type(screen.getByLabelText('Confirm password'), 'right-password');
    await userEvent.click(screen.getByRole('button', { name: 'Create owner account' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Workflows page' })).toBeInTheDocument());
    expect(calls.some((c) => c.url.endsWith('/auth/setup') && c.method === 'POST')).toBe(true);
    expect(calls.some((c) => c.url.endsWith('/auth/login') && c.method === 'POST')).toBe(true);
  });
});
