import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, buildUrl, listAll, setCsrfToken, setUnauthorizedHandler } from './client';
import { normalizeDoc } from './normalize';

const json = (body: unknown, init: ResponseInit = {}) =>
  new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' }, ...init });

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
  setCsrfToken(undefined);
});
afterEach(() => {
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
});

describe('buildUrl', () => {
  it('prefixes the API base and encodes queries, repeating array values', () => {
    expect(buildUrl('/workflows', { q: 'a b', status: ['error', 'success'], empty: '', none: undefined })).toBe(
      '/api/v1/workflows?q=a+b&status=error&status=success',
    );
  });
  it('does not prefix twice', () => {
    expect(buildUrl('/api/v1/me')).toBe('/api/v1/me');
  });
});

describe('request', () => {
  it('sends the CSRF header on mutations only', async () => {
    setCsrfToken('tok');
    fetchMock.mockImplementation(() => Promise.resolve(json({})));
    await api.get('/me');
    await api.post('/workflows', { a: 1 });
    const [, get] = fetchMock.mock.calls[0] as [string, RequestInit];
    const [, post] = fetchMock.mock.calls[1] as [string, RequestInit];
    expect((get.headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined();
    expect((post.headers as Record<string, string>)['X-CSRF-Token']).toBe('tok');
    expect((post.headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(post.credentials).toBe('same-origin');
  });

  it('turns problem+json into an ApiError with field errors', async () => {
    fetchMock.mockResolvedValue(
      json({ title: 'Unprocessable', status: 422, detail: 'bad', errors: [{ pointer: '/nodes/0', message: 'required' }] }, { status: 422 }),
    );
    const err = await api.post('/workflows', {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(422);
    expect((err as ApiError).message).toBe('bad');
    expect((err as ApiError).problem.errors?.[0]?.pointer).toBe('/nodes/0');
  });

  it('survives a non-JSON error body', async () => {
    fetchMock.mockResolvedValue(new Response('gateway', { status: 502, statusText: 'Bad Gateway' }));
    const err = (await api.get('/me').catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(502);
    expect(err.message).toBe('Bad Gateway');
  });

  it('reports a network failure as status 0', async () => {
    fetchMock.mockRejectedValue(new TypeError('failed'));
    const err = (await api.get('/me').catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(0);
  });

  it('calls the unauthorized handler on 401 outside /auth', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    fetchMock.mockResolvedValue(json({ title: 'Unauthorized', status: 401 }, { status: 401 }));
    await api.get('/workflows').catch(() => undefined);
    await api.post('/auth/login', {}).catch(() => undefined);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('returns undefined for 204', async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    await expect(api.del('/workflows/wf_1')).resolves.toBeUndefined();
  });
});

describe('listAll', () => {
  it('follows cursors until the last page', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ data: [1, 2], nextCursor: 'c1', hasMore: true }))
      .mockResolvedValueOnce(json({ data: [3], hasMore: false }));
    expect(await listAll<number>('/credentials')).toEqual([1, 2, 3]);
    expect(String((fetchMock.mock.calls[1] as [string])[0])).toContain('cursor=c1');
  });
});

describe('normalizeDoc', () => {
  it('fills the index and port the server omits', () => {
    const d = normalizeDoc({ name: 'x', nodes: [], connections: [{ from: { node: 'A', port: '', index: undefined as unknown as number }, to: { node: 'B', port: 'main', index: 2 } }] });
    expect(d.connections[0]).toEqual({ from: { node: 'A', port: 'main', index: 0 }, to: { node: 'B', port: 'main', index: 2 } });
  });
});
