import { API_BASE, CSRF_HEADER } from '../config';

export interface ProblemDetail {
  type?: string;
  title: string;
  status: number;
  detail?: string;
  requestId?: string;
  errors?: Array<{ pointer: string; message: string }>;
}

/** An RFC 9457 problem document (or a synthesized one for non-JSON failures). */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetail;
  constructor(problem: ProblemDetail) {
    super(problem.detail ?? problem.title);
    this.name = 'ApiError';
    this.status = problem.status;
    this.problem = problem;
  }
}

type Query = Record<string, string | number | boolean | undefined | Array<string | number>>;

export interface RequestOptions {
  query?: Query;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

let csrfToken: string | undefined;
let onUnauthorized: (() => void) | undefined;

export function setCsrfToken(token: string | undefined): void {
  csrfToken = token;
}
export function getCsrfToken(): string | undefined {
  return csrfToken;
}
/** Registers a callback for 401 answers on authenticated calls (session expired). */
export function setUnauthorizedHandler(fn: (() => void) | undefined): void {
  onUnauthorized = fn;
}

export function buildUrl(path: string, query?: Query): string {
  const url = path.startsWith('/') && !path.startsWith(API_BASE) ? `${API_BASE}${path}` : path;
  if (!query) return url;
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === '') continue;
    if (Array.isArray(v)) v.forEach((x) => sp.append(k, String(x)));
    else sp.set(k, String(v));
  }
  const qs = sp.toString();
  return qs ? `${url}?${qs}` : url;
}

async function toProblem(res: Response): Promise<ProblemDetail> {
  try {
    const body = (await res.json()) as Partial<ProblemDetail>;
    return { title: res.statusText, status: res.status, ...body };
  } catch {
    return { title: res.statusText || 'Request failed', status: res.status };
  }
}

const SAFE = new Set(['GET', 'HEAD']);

/** Performs a request and returns the parsed JSON (undefined for 204). Throws ApiError. */
export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await rawRequest(method, path, opts);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Like request but returns the Response (for ETag access and non-JSON bodies). */
export async function rawRequest(method: string, path: string, opts: RequestOptions = {}): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (!SAFE.has(method) && csrfToken) headers[CSRF_HEADER] = csrfToken;
  let res: Response;
  try {
    res = await fetch(buildUrl(path, opts.query), {
      method,
      headers,
      credentials: 'same-origin',
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      signal: opts.signal,
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e;
    throw new ApiError({ title: 'Network error', status: 0, detail: 'Cannot reach the server.' });
  }
  if (!res.ok) {
    const problem = await toProblem(res);
    if (res.status === 401 && !path.includes('/auth/')) onUnauthorized?.();
    throw new ApiError(problem);
  }
  return res;
}

export const api = {
  get: <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>('GET', path, { query, signal }),
  post: <T>(path: string, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal) => request<T>('POST', path, { body, headers, signal }),
  put: <T>(path: string, body?: unknown, headers?: Record<string, string>) => request<T>('PUT', path, { body, headers }),
  del: <T = void>(path: string) => request<T>('DELETE', path),
};

/** Walks every page of a cursor-paginated list (use sparingly; prefer infinite queries). */
export async function listAll<T>(path: string, query: Query = {}, maxPages = 20): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const page = await api.get<{ data: T[]; nextCursor?: string; hasMore: boolean }>(path, { ...query, cursor });
    out.push(...page.data);
    if (!page.hasMore || !page.nextCursor) break;
    cursor = page.nextCursor;
  }
  return out;
}
