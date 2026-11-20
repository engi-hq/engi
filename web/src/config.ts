// Runtime constants. The API is same-origin; the base path can be overridden for tests or proxies.
export const API_BASE: string = import.meta.env.VITE_API_BASE ?? '/api/v1';
export const CSRF_HEADER = 'X-CSRF-Token';
export const MAX_RECENT_NODES = 8;
/** Largest binary value (bytes) the run view fetches to preview as text. Larger values offer download only. */
export const BINARY_PREVIEW_MAX_BYTES = 512 * 1024;
/** How often the Executions page refetches while "Live" is on (ms). */
export const EXECUTIONS_REFRESH_MS = 5000;
/** Smallest width (percent of the execution span) a timeline bar is drawn with, so 0 ms runs stay visible. */
export const TIMELINE_MIN_BAR_PCT = 0.5;
