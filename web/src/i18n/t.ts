import { en, type MessageKey } from './en';

/** Looks up a UI string. `{name}` placeholders are replaced from vars. English is the only catalogue so far. */
export function t(key: MessageKey, vars?: Record<string, string | number>): string {
  const msg: string = en[key];
  if (!vars) return msg;
  return msg.replace(/\{(\w+)\}/g, (_, k: string) => String(vars[k] ?? `{${k}}`));
}

type PluralBase = { [K in MessageKey]: K extends `${infer B}.one` ? B : never }[MessageKey];

/** Plural-aware lookup: uses `<key>.one` for 1 and `<key>.other` otherwise. */
export function tn(key: PluralBase, n: number): string {
  return t(`${key}.${n === 1 ? 'one' : 'other'}` as MessageKey, { n });
}
