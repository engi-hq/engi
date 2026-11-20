import { describe, expect, it } from 'vitest';
import type { Property } from '../api/types';
import { defaultParameters, effectiveValue, fromExpression, isVisible, toExpression, validate } from './logic';

const body: Property = { name: 'body', type: 'string', displayOptions: { show: { method: ['POST', 'PUT'] } } };
const method: Property = { name: 'method', type: 'options', default: 'GET', options: [{ name: 'GET', value: 'GET' }, { name: 'POST', value: 'POST' }] };

describe('displayOptions', () => {
  it('shows a field when a sibling holds one of the listed values', () => {
    expect(isVisible(body, [method, body], { method: 'POST' })).toBe(true);
    expect(isVisible(body, [method, body], { method: 'GET' })).toBe(false);
  });
  it('uses the sibling default when the value is unset', () => {
    expect(isVisible(body, [method, body], {})).toBe(false);
  });
  it('hide wins over show', () => {
    const p: Property = { name: 'x', type: 'string', displayOptions: { hide: { method: ['GET'] } } };
    expect(isVisible(p, [method, p], {})).toBe(false);
    expect(isVisible(p, [method, p], { method: 'POST' })).toBe(true);
  });
  it('requires every show key to match', () => {
    const p: Property = { name: 'x', type: 'string', displayOptions: { show: { a: ['1'], b: ['2'] } } };
    const a: Property = { name: 'a', type: 'string' };
    const b: Property = { name: 'b', type: 'string' };
    expect(isVisible(p, [a, b, p], { a: '1', b: '2' })).toBe(true);
    expect(isVisible(p, [a, b, p], { a: '1', b: '3' })).toBe(false);
  });
  it('compares numbers and booleans by their string form', () => {
    const p: Property = { name: 'x', type: 'string', displayOptions: { show: { flag: [true], n: [3] } } };
    expect(isVisible(p, [p], { flag: true, n: 3 })).toBe(true);
    expect(isVisible(p, [p], { flag: false, n: 3 })).toBe(false);
  });
  it('falls back to root parameters for nested fields', () => {
    const nested: Property = { name: 'n', type: 'string', displayOptions: { show: { mode: ['advanced'] } } };
    expect(isVisible(nested, [nested], {}, { mode: 'advanced' })).toBe(true);
    expect(isVisible(nested, [nested], {}, { mode: 'basic' })).toBe(false);
  });
});

describe('validate and defaults', () => {
  const props: Property[] = [
    { name: 'url', type: 'string', required: true },
    { name: 'secret', type: 'string', required: true, displayOptions: { show: { auth: ['yes'] } } },
    { name: 'auth', type: 'string', default: 'no' },
    { name: 'rows', type: 'fixedCollection', children: [{ name: 'k', type: 'string', required: true }] },
    { name: 'flag', type: 'boolean', required: true },
  ];
  it('reports empty required fields and skips hidden ones', () => {
    expect(validate(props, {})).toEqual({ url: 'required' });
    expect(validate(props, { url: 'x', auth: 'yes' })).toEqual({ secret: 'required' });
  });
  it('validates collection rows by path', () => {
    expect(validate(props, { url: 'x', rows: [{ k: 'a' }, {}] })).toEqual({ 'rows.1.k': 'required' });
  });
  it('treats false as a value for required booleans', () => {
    expect(validate(props, { url: 'x', flag: false })).toEqual({});
  });
  it('collects defaults for new nodes', () => {
    expect(defaultParameters(props)).toEqual({ auth: 'no' });
    expect(effectiveValue(method, {})).toBe('GET');
  });
});

describe('expression mode conversion', () => {
  it('prefixes values with =', () => {
    expect(toExpression('abc')).toBe('=abc');
    expect(toExpression(5)).toBe('=5');
    expect(toExpression(undefined)).toBe('=');
    expect(toExpression({ a: 1 })).toBe('={"a":1}');
  });
  it('converts back by property type', () => {
    expect(fromExpression('=12', { name: 'n', type: 'number' })).toBe(12);
    expect(fromExpression('={{ $json.n }}', { name: 'n', type: 'number', default: 7 })).toBe(7);
    expect(fromExpression('=true', { name: 'b', type: 'boolean' })).toBe(true);
    expect(fromExpression('=POST', method)).toBe('POST');
    expect(fromExpression('=hello', { name: 's', type: 'string' })).toBe('hello');
  });
});
