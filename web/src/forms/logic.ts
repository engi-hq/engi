import type { DisplayRule, Property } from '../api/types';

export type Values = Record<string, unknown>;

export const isExpression = (v: unknown): v is string => typeof v === 'string' && v.startsWith('=');

/** The value shown for a property: the stored value, else its default. */
export function effectiveValue(prop: Property, values: Values): unknown {
  const v = values[prop.name];
  return v === undefined ? prop.default : v;
}

function matches(rule: DisplayRule, props: Property[], values: Values, root: Values): boolean {
  return Object.entries(rule).every(([name, wanted]) => {
    const sibling = props.find((p) => p.name === name);
    const raw = name in values ? values[name] : sibling ? sibling.default : root[name];
    return wanted.some((w) => w === raw || String(w) === String(raw));
  });
}

/**
 * displayOptions semantics: every key of `show` must match one of its values;
 * any key of `hide` matching hides the field. Siblings are checked first, then the
 * root parameters (for fields nested in a collection).
 */
export function isVisible(prop: Property, siblings: Property[], values: Values, root: Values = values): boolean {
  const d = prop.displayOptions;
  if (!d) return true;
  if (d.show && Object.keys(d.show).length > 0 && !matches(d.show, siblings, values, root)) return false;
  if (d.hide) {
    for (const [name, wanted] of Object.entries(d.hide)) {
      if (matches({ [name]: wanted }, siblings, values, root)) return false;
    }
  }
  return true;
}

export const isEmpty = (v: unknown): boolean =>
  v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0);

/** Required-field problems keyed by dotted path. Hidden fields are skipped. */
export function validate(props: Property[], values: Values, root: Values = values, prefix = ''): Record<string, string> {
  const errs: Record<string, string> = {};
  for (const p of props) {
    if (p.type === 'notice' || !isVisible(p, props, values, root)) continue;
    const path = prefix + p.name;
    const v = effectiveValue(p, values);
    if (p.required && p.type !== 'boolean' && isEmpty(v)) errs[path] = 'required';
    if (p.type === 'fixedCollection' && Array.isArray(v) && p.children) {
      v.forEach((row, i) => Object.assign(errs, validate(p.children ?? [], row as Values, root, `${path}.${i}.`)));
    }
    if (p.type === 'collection' && v && typeof v === 'object' && p.children) {
      Object.assign(errs, validate(p.children, v as Values, root, `${path}.`));
    }
  }
  return errs;
}

/** Default parameters for a new node: defaults of the top-level properties that have one. */
export function defaultParameters(props: Property[]): Values {
  const out: Values = {};
  for (const p of props) {
    if (p.default !== undefined && p.type !== 'notice') out[p.name] = p.default;
  }
  return out;
}

/** Switches a value between fixed and expression mode, converting where it is lossless. */
export function toExpression(v: unknown): string {
  if (typeof v === 'string') return `=${v}`;
  if (v === undefined || v === null) return '=';
  return `=${typeof v === 'object' ? JSON.stringify(v) : String(v)}`;
}

export function fromExpression(expr: string, prop: Property): unknown {
  const body = expr.startsWith('=') ? expr.slice(1) : expr;
  switch (prop.type) {
    case 'number': {
      const n = Number(body);
      return body.trim() !== '' && Number.isFinite(n) ? n : (prop.default ?? 0);
    }
    case 'boolean':
      return body.trim() === 'true' ? true : body.trim() === 'false' ? false : (prop.default ?? false);
    case 'options': {
      const hit = prop.options?.find((o) => String(o.value) === body);
      return hit ? hit.value : (prop.default ?? '');
    }
    default:
      return body;
  }
}
