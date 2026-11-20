import { Fragment, type ReactNode } from 'react';

/**
 * A small markdown renderer for sticky notes. It builds React elements and never sets HTML, so raw tags in the
 * text show up as text, and links are kept only for a short list of safe schemes.
 * Supported: headings, paragraphs, bullet and numbered lists, block quotes, fenced and inline code, rules,
 * bold, italic, strikethrough and links. Images are not rendered.
 */

const SAFE_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

/** Returns the href when its scheme is allowed, else null. Relative and scheme-less URLs are refused too. */
export function safeHref(raw: string): string | null {
  const href = raw.trim();
  try {
    const u = new URL(href);
    return SAFE_SCHEMES.has(u.protocol) ? href : null;
  } catch {
    return null;
  }
}

const isWordChar = (c: string | undefined): boolean => !!c && /[\p{L}\p{N}]/u.test(c);

function inline(src: string, keyPrefix: string): ReactNode[] {
  const out: ReactNode[] = [];
  let text = '';
  let n = 0;
  const key = () => `${keyPrefix}.${n++}`;
  const flush = () => { if (text) { out.push(text); text = ''; } };
  let i = 0;
  while (i < src.length) {
    const c = src[i] as string;
    if (c === '\\' && i + 1 < src.length) { text += src[i + 1]; i += 2; continue; }
    if (c === '`') {
      const end = src.indexOf('`', i + 1);
      if (end > i + 1) { flush(); out.push(<code key={key()}>{src.slice(i + 1, end)}</code>); i = end + 1; continue; }
    }
    if (c === '[') {
      const m = /^\[([^\]]*)\]\(([^)\s]*)\)/.exec(src.slice(i));
      if (m) {
        flush();
        const href = safeHref(m[2] as string);
        const label = inline(m[1] as string, key());
        out.push(href
          ? <a key={key()} href={href} target="_blank" rel="noopener noreferrer nofollow">{label}</a>
          : <Fragment key={key()}>{label}</Fragment>);
        i += m[0].length;
        continue;
      }
    }
    const double = src.startsWith('**', i) ? '**' : src.startsWith('~~', i) ? '~~' : '';
    if (double) {
      const end = src.indexOf(double, i + 2);
      if (end > i + 2) {
        flush();
        const inner = inline(src.slice(i + 2, end), key());
        out.push(double === '**' ? <strong key={key()}>{inner}</strong> : <del key={key()}>{inner}</del>);
        i = end + 2;
        continue;
      }
    }
    if ((c === '*' || c === '_') && !(c === '_' && isWordChar(src[i - 1])) && src[i + 1] !== ' ' && src[i + 1] !== c) {
      const end = src.indexOf(c, i + 1);
      if (end > i + 1 && src[end - 1] !== ' ' && !(c === '_' && isWordChar(src[end + 1]))) {
        flush();
        out.push(<em key={key()}>{inline(src.slice(i + 1, end), key())}</em>);
        i = end + 1;
        continue;
      }
    }
    text += c;
    i++;
  }
  flush();
  return out;
}

type Block =
  | { kind: 'heading'; level: number; text: string }
  | { kind: 'code'; text: string }
  | { kind: 'rule' }
  | { kind: 'quote'; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'paragraph'; text: string };

const BULLET = /^\s{0,3}[-*+]\s+(.*)$/;
const ORDERED = /^\s{0,3}\d+[.)]\s+(.*)$/;
const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const RULE = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;

function blocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  const out: Block[] = [];
  let i = 0;
  const startsBlock = (l: string) => HEADING.test(l) || BULLET.test(l) || ORDERED.test(l) || l.startsWith('>') || l.startsWith('```') || RULE.test(l);
  while (i < lines.length) {
    const line = lines[i] as string;
    if (line.trim() === '') { i++; continue; }
    if (line.startsWith('```')) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] as string).startsWith('```')) body.push(lines[i++] as string);
      i++;
      out.push({ kind: 'code', text: body.join('\n') });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) { out.push({ kind: 'heading', level: (h[1] as string).length, text: h[2] as string }); i++; continue; }
    if (RULE.test(line)) { out.push({ kind: 'rule' }); i++; continue; }
    if (line.startsWith('>')) {
      const body: string[] = [];
      while (i < lines.length && (lines[i] as string).startsWith('>')) body.push((lines[i++] as string).replace(/^>\s?/, ''));
      out.push({ kind: 'quote', text: body.join('\n') });
      continue;
    }
    const ordered = ORDERED.test(line);
    if (ordered || BULLET.test(line)) {
      const re = ordered ? ORDERED : BULLET;
      const items: string[] = [];
      while (i < lines.length) {
        const m = re.exec(lines[i] as string);
        if (!m) break;
        items.push(m[1] as string);
        i++;
      }
      out.push({ kind: 'list', ordered, items });
      continue;
    }
    const para: string[] = [line];
    i++;
    while (i < lines.length && (lines[i] as string).trim() !== '' && !startsBlock(lines[i] as string)) para.push(lines[i++] as string);
    out.push({ kind: 'paragraph', text: para.join('\n') });
  }
  return out;
}

function render(b: Block, k: string): ReactNode {
  switch (b.kind) {
    case 'heading': {
      const Tag = `h${Math.min(b.level, 6)}` as 'h1';
      return <Tag key={k}>{inline(b.text, k)}</Tag>;
    }
    case 'code': return <pre key={k}><code>{b.text}</code></pre>;
    case 'rule': return <hr key={k} />;
    case 'quote': return <blockquote key={k}>{blocks(b.text).map((x, j) => render(x, `${k}.${j}`))}</blockquote>;
    case 'list': {
      const Tag = b.ordered ? 'ol' : 'ul';
      return <Tag key={k}>{b.items.map((it, j) => <li key={j}>{inline(it, `${k}.${j}`)}</li>)}</Tag>;
    }
    case 'paragraph': return <p key={k}>{inline(b.text, k)}</p>;
  }
}

export function Markdown({ source, className }: { source: string; className?: string }) {
  return <div className={className}>{blocks(source).map((b, i) => render(b, `b${i}`))}</div>;
}
