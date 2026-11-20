import { buildUrl } from '../api/client';

/** A binary value of an item as the API returns it. */
export interface BinaryValue {
  ref: string;
  mime: string;
  name: string;
  size: number;
}

export type PreviewKind = 'image' | 'audio' | 'video' | 'text' | 'pdf' | 'none';

const BLOB_SCHEME = 'blob://';
/** The server never serves SVG in place (it can carry script), so it is not previewed either. */
const IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

/** Reads one entry of `item.binary`; returns null when it has no usable reference. */
export function parseBinary(v: unknown): BinaryValue | null {
  if (typeof v !== 'object' || v === null) return null;
  const o = v as Record<string, unknown>;
  if (typeof o['ref'] !== 'string' || o['ref'] === '') return null;
  return {
    ref: o['ref'],
    mime: typeof o['mime'] === 'string' ? o['mime'] : '',
    name: typeof o['name'] === 'string' ? o['name'] : '',
    size: typeof o['size'] === 'number' ? o['size'] : 0,
  };
}

/** How a value can be previewed, from its MIME type alone. */
export function previewKind(mime: string): PreviewKind {
  const base = (mime.split(';')[0] ?? '').trim().toLowerCase();
  if (base.startsWith('image/')) return IMAGE_TYPES.has(base) ? 'image' : 'none';
  if (base.startsWith('audio/')) return 'audio';
  if (base.startsWith('video/')) return 'video';
  if (base === 'application/pdf') return 'pdf';
  if (base.startsWith('text/') || base === 'application/json' || base === 'application/xml' || base.endsWith('+json') || base.endsWith('+xml')) return 'text';
  return 'none';
}

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];
const UNIT_STEP = 1024;

/** 1536 becomes "1.5 KB". */
export function humanSize(bytes: number): string {
  let n = Math.max(0, bytes);
  let u = 0;
  while (n >= UNIT_STEP && u < UNITS.length - 1) { n /= UNIT_STEP; u++; }
  return `${u === 0 ? n : n.toFixed(n < 10 ? 1 : 0)} ${UNITS[u]}`;
}

/**
 * URL of the binary download endpoint for a value. A full `blob://<execution>/<id>` reference
 * names its own execution; a bare id belongs to `executionId`. Returns null when neither is known.
 */
export function binaryUrl(ref: string, executionId: string | undefined, inline = false): string | null {
  let exec = executionId;
  let id = ref;
  if (ref.startsWith(BLOB_SCHEME)) {
    const [e, i] = ref.slice(BLOB_SCHEME.length).split('/');
    if (!e || !i) return null;
    exec = e;
    id = i;
  }
  if (!exec || !id) return null;
  return buildUrl(`/executions/${encodeURIComponent(exec)}/binary/${encodeURIComponent(id)}`, inline ? { inline: true } : undefined);
}

/** Fetches a binary value with the session and decodes at most `max` bytes as UTF-8 text. */
export async function fetchBinaryText(url: string, max: number, signal?: AbortSignal): Promise<{ text: string; truncated: boolean }> {
  const res = await fetch(url, { credentials: 'same-origin', signal });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`.trim());
  const buf = await res.arrayBuffer();
  const truncated = buf.byteLength > max;
  return { text: new TextDecoder().decode(truncated ? buf.slice(0, max) : buf), truncated };
}
