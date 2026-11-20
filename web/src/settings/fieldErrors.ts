import { ApiError } from '../api/client';

/** Maps a problem+json `errors[]` list to `{ field: message }` using the last pointer segment. */
export function fieldErrors(e: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (e instanceof ApiError) {
    for (const x of e.problem.errors ?? []) {
      const key = x.pointer.split('/').filter(Boolean).pop();
      if (key) out[key] = x.message;
    }
  }
  return out;
}
