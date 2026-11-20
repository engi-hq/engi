import { describe, expect, it } from 'vitest';
import { previewExpression, walkPath } from './preview';

const scope = { json: { name: 'Ada', n: 3, tags: ['a', 'b'], nested: { 'odd key': 1 } }, nodes: { Fetch: { id: 7 } } };

describe('previewExpression', () => {
  it('keeps the type of a single expression', () => {
    expect(previewExpression('={{ $json.n }}', scope)).toEqual({ ok: true, value: '3' });
    expect(previewExpression('={{ $json.tags }}', scope)).toEqual({ ok: true, value: '["a","b"]' });
  });
  it('interpolates text around expressions', () => {
    expect(previewExpression('=Hello {{ $json.name }}!', scope)).toEqual({ ok: true, value: 'Hello Ada!' });
  });
  it('reads array indexes and bracket keys', () => {
    expect(previewExpression('={{ $json.tags[1] }}', scope)).toEqual({ ok: true, value: 'b' });
    expect(previewExpression('={{ $json.nested["odd key"] }}', scope)).toEqual({ ok: true, value: '1' });
  });
  it('reads other nodes', () => {
    expect(previewExpression('={{ $("Fetch").item.json.id }}', scope)).toEqual({ ok: true, value: '7' });
    expect(previewExpression('={{ $node["Fetch"].json.id }}', scope)).toEqual({ ok: true, value: '7' });
    expect(previewExpression('={{ $("Nope").item.json.id }}', scope)).toEqual({ ok: false, reason: 'missing' });
  });
  it('never evaluates code', () => {
    expect(previewExpression('={{ $json.n + 1 }}', scope)).toEqual({ ok: false, reason: 'complex' });
    expect(previewExpression('={{ alert(1) }}', scope)).toEqual({ ok: false, reason: 'complex' });
  });
  it('reports missing paths and empty input', () => {
    expect(previewExpression('={{ $json.nope }}', scope)).toEqual({ ok: false, reason: 'missing' });
    expect(previewExpression('=', scope)).toEqual({ ok: false, reason: 'empty' });
  });
  it('returns plain text unchanged', () => {
    expect(previewExpression('=just text', scope)).toEqual({ ok: true, value: 'just text' });
  });
  it('does not follow prototype properties', () => {
    expect(walkPath({}, '.constructor').found).toBe(false);
  });
});
