import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { Property } from '../api/types';
import { emptyEnv, FormEnvContext } from './env';
import type { Values } from './logic';
import { PropertyForm } from './PropertyForm';

// The code editor is lazy and needs real layout; a textarea stands in for it.
vi.mock('./LazyCode', () => ({
  LazyCode: (p: { value: string; onChange: (v: string) => void; ariaLabel: string }) => (
    <textarea aria-label={p.ariaLabel} value={p.value} onChange={(e) => p.onChange(e.target.value)} />
  ),
}));

const props: Property[] = [
  { name: 'method', type: 'options', displayName: 'Method', default: 'GET', options: [{ name: 'GET', value: 'GET' }, { name: 'POST', value: 'POST' }] },
  { name: 'url', type: 'string', displayName: 'URL', required: true },
  { name: 'body', type: 'json', displayName: 'Body', displayOptions: { show: { method: ['POST'] } } },
  { name: 'retries', type: 'number', displayName: 'Retries', default: 0 },
  { name: 'follow', type: 'boolean', displayName: 'Follow redirects', default: true },
  { name: 'tags', type: 'multiOptions', displayName: 'Tags', options: [{ name: 'A', value: 'a' }, { name: 'B', value: 'b' }] },
  { name: 'info', type: 'notice', description: 'Heads up' },
  {
    name: 'headers', type: 'fixedCollection', displayName: 'Headers',
    children: [{ name: 'name', type: 'string', displayName: 'Name' }, { name: 'value', type: 'string', displayName: 'Value' }],
  },
];

function Harness({ initial = {}, errors }: { initial?: Values; errors?: Record<string, string> }) {
  const [values, setValues] = useState<Values>(initial);
  return (
    <FormEnvContext.Provider value={emptyEnv}>
      <PropertyForm properties={props} values={values} onChange={setValues} errors={errors} />
      <pre data-testid="out">{JSON.stringify(values)}</pre>
    </FormEnvContext.Provider>
  );
}
const out = () => JSON.parse(screen.getByTestId('out').textContent ?? '{}') as Values;

describe('PropertyForm', () => {
  it('renders each property type from the descriptor', () => {
    render(<Harness />);
    expect(screen.getByLabelText(/^URL/)).toBeInTheDocument();
    expect(screen.getByLabelText('Method')).toHaveValue('GET');
    expect(screen.getByLabelText('Retries')).toHaveValue(0);
    expect(screen.getByRole('switch', { name: 'Follow redirects' })).toBeChecked();
    expect(screen.getByRole('group', { name: 'Tags' })).toBeInTheDocument();
    expect(screen.getByRole('note')).toHaveTextContent('Heads up');
    expect(screen.getByRole('button', { name: /add item/i })).toBeInTheDocument();
  });

  it('shows and hides fields by displayOptions as values change', async () => {
    render(<Harness />);
    expect(screen.queryByLabelText('Body')).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('Method'), 'POST');
    expect(screen.getByLabelText('Body')).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('Method'), 'GET');
    expect(screen.queryByLabelText('Body')).not.toBeInTheDocument();
  });

  it('keeps the value of a hidden field', async () => {
    render(<Harness initial={{ method: 'POST', body: '{"a":1}' }} />);
    await userEvent.selectOptions(screen.getByLabelText('Method'), 'GET');
    expect(out()['body']).toBe('{"a":1}');
  });

  it('edits strings, numbers, booleans and multi-options', async () => {
    render(<Harness />);
    await userEvent.type(screen.getByLabelText(/^URL/), 'x');
    await userEvent.clear(screen.getByLabelText('Retries'));
    await userEvent.type(screen.getByLabelText('Retries'), '3');
    await userEvent.click(screen.getByRole('switch', { name: 'Follow redirects' }));
    await userEvent.click(screen.getByLabelText('B'));
    expect(out()).toMatchObject({ url: 'x', retries: 3, follow: false, tags: ['b'] });
  });

  it('adds, edits and removes fixed-collection rows', async () => {
    render(<Harness />);
    await userEvent.click(screen.getByRole('button', { name: /add item/i }));
    const row = screen.getByRole('group', { name: 'Item 1' });
    await userEvent.type(within(row).getByLabelText('Name'), 'X-Id');
    expect(out()['headers']).toEqual([{ name: 'X-Id' }]);
    await userEvent.click(screen.getByRole('button', { name: 'Remove item 1' }));
    expect(out()['headers']).toEqual([]);
  });

  it('toggles a field into expression mode and back', async () => {
    render(<Harness initial={{ url: 'abc' }} />);
    const urlField = screen.getByLabelText(/^URL/).closest('.pfield') as HTMLElement;
    await userEvent.click(within(urlField).getByRole('button', { name: 'Expression' }));
    expect(out()['url']).toBe('=abc');
    expect(within(urlField).getByLabelText('Expression for URL')).toHaveValue('abc');
    await userEvent.type(within(urlField).getByLabelText('Expression for URL'), '{{');
    expect((out()['url'] as string).startsWith('=abc')).toBe(true);
    await userEvent.click(within(urlField).getByRole('button', { name: 'Fixed' }));
    expect(typeof out()['url']).toBe('string');
    expect((out()['url'] as string).startsWith('=')).toBe(false);
  });

  it('marks required fields and shows errors', () => {
    render(<Harness errors={{ url: 'required' }} />);
    expect(screen.getByRole('alert')).toHaveTextContent('URL is required');
    expect(screen.getByLabelText(/^URL/)).toHaveAttribute('aria-invalid', 'true');
  });

  it('renders read-only without edit controls', () => {
    render(
      <FormEnvContext.Provider value={{ ...emptyEnv, readOnly: true }}>
        <PropertyForm properties={props} values={{}} onChange={() => undefined} />
      </FormEnvContext.Provider>,
    );
    expect(screen.getByLabelText(/^URL/)).toBeDisabled();
    expect(screen.queryByRole('button', { name: /add item/i })).not.toBeInTheDocument();
  });
});
