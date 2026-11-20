import { MAX_RECENT_NODES } from '../config';
import type { NodeType } from '../api/types';

export const GROUP_ORDER = ['trigger', 'action', 'transform', 'flow', 'ai', 'code'];

/** Subsequence match score; higher is better, 0 means no match. */
export function fuzzyScore(query: string, text: string): number {
  const q = query.toLowerCase().trim();
  if (!q) return 1;
  const s = text.toLowerCase();
  const at = s.indexOf(q);
  if (at >= 0) return 100 - Math.min(at, 50) + (at === 0 ? 20 : 0);
  let qi = 0;
  let score = 0;
  let last = -2;
  for (let i = 0; i < s.length && qi < q.length; i++) {
    if (s[i] === q[qi]) {
      score += last === i - 1 ? 3 : 1;
      last = i;
      qi++;
    }
  }
  return qi === q.length ? score : 0;
}

export function searchNodeTypes(types: NodeType[], query: string, recent: string[] = []): NodeType[] {
  const scored = types
    .map((nt) => {
      const s = Math.max(
        fuzzyScore(query, nt.displayName) * 2,
        fuzzyScore(query, nt.group),
        fuzzyScore(query, nt.description ?? '') / 2,
        fuzzyScore(query, nt.type),
      );
      const r = recent.indexOf(nt.type);
      return { nt, s: s + (query ? 0 : r >= 0 ? 50 - r : 0) };
    })
    .filter((x) => x.s > 0);
  scored.sort((a, b) => b.s - a.s || a.nt.displayName.localeCompare(b.nt.displayName));
  return scored.map((x) => x.nt);
}

const KEY = 'engi.recentNodes';

export function loadRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]') as unknown;
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
  } catch {
    return [];
  }
}

export function rememberNode(type: string): string[] {
  const next = [type, ...loadRecent().filter((x) => x !== type)].slice(0, MAX_RECENT_NODES);
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* storage may be blocked */
  }
  return next;
}
