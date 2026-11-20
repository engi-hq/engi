import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import type { Item } from '../api/types';
import { cellText, columnsOf, DataView } from './DataView';

const items: Item[] = [{ json: { id: 1, name: 'Ada', tags: ['a'] } }, { json: { id: 2, extra: true } }];

describe('DataView', () => {
  it('shows a table with the union of columns', () => {
    render(<DataView items={items} label="Output" />);
    expect(screen.getByRole('columnheader', { name: 'extra' })).toBeInTheDocument();
    expect(screen.getByText('Ada')).toBeInTheDocument();
    expect(screen.getByText('2 items')).toBeInTheDocument();
  });
  it('switches to a collapsible JSON tree with a filter', async () => {
    render(<DataView items={items} label="Output" />);
    await userEvent.click(screen.getByRole('button', { name: 'JSON' }));
    expect(screen.getByRole('tree', { name: 'JSON' })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText('Filter keys and values'), 'ada');
    expect(screen.getByText('"Ada"')).toBeInTheDocument();
    expect(screen.queryByText('extra')).not.toBeInTheDocument();
  });
  it('pages long lists', async () => {
    const many = Array.from({ length: 60 }, (_, i) => ({ json: { i } }));
    render(<DataView items={many} label="Output" />);
    expect(screen.getByText('1 / 3')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Next page' }));
    expect(screen.getByText('2 / 3')).toBeInTheDocument();
    expect(screen.getByRole('rowheader', { name: '25' })).toBeInTheDocument();
  });
  it('says so when there are no items', () => {
    render(<DataView items={[]} label="Output" />);
    expect(screen.getByText('No items.')).toBeInTheDocument();
  });
  it('formats cells and caps columns', () => {
    expect(cellText(null)).toBe('null');
    expect(cellText({ a: 1 })).toBe('{"a":1}');
    expect(cellText('x'.repeat(10))).toBe('xxxxxxxxxx');
    const wide = [{ json: Object.fromEntries(Array.from({ length: 40 }, (_, i) => [`k${i}`, i])) }];
    expect(columnsOf(wide)).toMatchObject({ truncated: true });
    expect(columnsOf(wide).cols).toHaveLength(24);
  });
});
