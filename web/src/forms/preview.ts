// Client-side preview of expressions. The server has no preview endpoint yet, and running
// user code in the browser is not an option, so only plain data paths are resolved:
// {{ $json.a.b[0] }}, {{ $("Node").item.json.x }}, {{ $node["Node"].json.x }}.
// Anything else is reported as "evaluated at run time".

export interface PreviewScope {
  /** json of the selected input item */
  json?: Record<string, unknown>;
  /** last-run output items per node name (first item is used) */
  nodes?: Record<string, Record<string, unknown> | undefined>;
}

export type PreviewResult = { ok: true; value: string } | { ok: false; reason: 'complex' | 'missing' | 'empty' };

const SEGMENT = /\.([A-Za-z_$][\w$]*)|\[(\d+)\]|\["((?:[^"\\]|\\.)*)"\]|\['((?:[^'\\]|\\.)*)'\]/y;

export function walkPath(root: unknown, path: string): { found: boolean; value?: unknown } {
  let cur = root;
  let pos = 0;
  while (pos < path.length) {
    SEGMENT.lastIndex = pos;
    const m = SEGMENT.exec(path);
    if (!m) return { found: false };
    pos = SEGMENT.lastIndex;
    const key = m[1] ?? m[3] ?? m[4];
    if (cur === null || typeof cur !== 'object') return { found: false };
    if (m[2] !== undefined) {
      if (!Array.isArray(cur)) return { found: false };
      cur = cur[Number(m[2])];
    } else {
      if (Array.isArray(cur) || !Object.hasOwn(cur as object, key as string)) return { found: false };
      cur = (cur as Record<string, unknown>)[key!];
    }
  }
  return { found: true, value: cur };
}

const stringify = (v: unknown): string =>
  typeof v === 'string' ? v : v === undefined ? 'undefined' : JSON.stringify(v);

function resolveInner(inner: string, scope: PreviewScope): { found: boolean; value?: unknown } | 'complex' {
  const src = inner.trim();
  let m = /^\$json((?:\.[A-Za-z_$][\w$]*|\[\d+\]|\["(?:[^"\\]|\\.)*"\]|\['(?:[^'\\]|\\.)*'\])*)$/.exec(src);
  if (m) return m[1] ? walkPath(scope.json ?? {}, m[1]) : { found: true, value: scope.json ?? {} };
  m = /^\$\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')\s*\)\.(?:item|first\(\)|last\(\))\.json((?:\.[A-Za-z_$][\w$]*|\[\d+\]|\["[^"]*"\])*)$/.exec(src)
    ?? /^\$node\[\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')\s*\]\.json((?:\.[A-Za-z_$][\w$]*|\[\d+\]|\["[^"]*"\])*)$/.exec(src);
  if (m) {
    const name = (m[1] ?? m[2]) as string;
    const json = scope.nodes?.[name];
    if (json === undefined) return { found: false };
    return m[3] ? walkPath(json, m[3]) : { found: true, value: json };
  }
  return 'complex';
}

/** Previews a stored value (with its leading "="). */
export function previewExpression(expr: string, scope: PreviewScope): PreviewResult {
  const body = expr.startsWith('=') ? expr.slice(1) : expr;
  if (body.trim() === '') return { ok: false, reason: 'empty' };
  const parts = body.split(/(\{\{[\s\S]*?\}\})/g);
  const hasExpr = parts.some((p) => p.startsWith('{{'));
  if (!hasExpr) return { ok: true, value: body };
  const whole = parts.filter((p) => p !== '');
  let out = '';
  for (const part of parts) {
    if (!part.startsWith('{{')) {
      out += part;
      continue;
    }
    const r = resolveInner(part.slice(2, -2), scope);
    if (r === 'complex') return { ok: false, reason: 'complex' };
    if (!r.found) return { ok: false, reason: 'missing' };
    // A template that is exactly one expression keeps the value's type, shown as JSON.
    out += whole.length === 1 ? stringify(r.value) : typeof r.value === 'string' ? r.value : stringify(r.value);
  }
  return { ok: true, value: out };
}
