import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { WorkflowDoc } from '../api/types';
import { parsePinned, setPin } from './model';
import { PinControls } from './PinControls';

const doc: WorkflowDoc = { name: 'w', nodes: [], connections: [] };

describe('pin model', () => {
  it('pins and unpins', () => {
    const pinned = setPin(doc, 'A', [{ x: 1 }]);
    expect(pinned.pinData).toEqual({ A: [{ x: 1 }] });
    expect(setPin(pinned, 'A', undefined).pinData).toBeUndefined();
  });
  it('validates an array of objects', () => {
    expect(parsePinned('[{"a":1}]')).toEqual({ ok: true, items: [{ a: 1 }] });
    expect(parsePinned('{')).toEqual({ ok: false, reason: 'syntax' });
    expect(parsePinned('{"a":1}')).toEqual({ ok: false, reason: 'array' });
    expect(parsePinned('[1]')).toEqual({ ok: false, reason: 'objects' });
  });
});

describe('PinControls', () => {
  it('pins the output and unpins', async () => {
    const onPin = vi.fn();
    const { rerender } = render(<PinControls output={[{ a: 1 }]} readOnly={false} onPin={onPin} />);
    await userEvent.click(screen.getByRole('button', { name: 'Pin output' }));
    expect(onPin).toHaveBeenCalledWith([{ a: 1 }]);
    rerender(<PinControls output={[{ a: 1 }]} pinned={[{ a: 1 }]} readOnly={false} onPin={onPin} />);
    await userEvent.click(screen.getByRole('button', { name: 'Unpin' }));
    expect(onPin).toHaveBeenLastCalledWith(undefined);
  });
  it('rejects invalid edits and saves valid ones', async () => {
    const onPin = vi.fn();
    render(<PinControls output={[]} pinned={[{ a: 1 }]} readOnly={false} onPin={onPin} />);
    await userEvent.click(screen.getByRole('button', { name: 'Edit pinned data' }));
    const box = screen.getByLabelText('Pinned data (JSON array of objects)');
    await userEvent.clear(box);
    await userEvent.type(box, '[[1]');
    await userEvent.click(screen.getByRole('button', { name: 'Save pinned data' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Every pinned item must be an object.');
    expect(onPin).not.toHaveBeenCalled();
    await userEvent.clear(box);
    await userEvent.type(box, '[[{{"b":2}]');
    await userEvent.click(screen.getByRole('button', { name: 'Save pinned data' }));
    expect(onPin).toHaveBeenCalledWith([{ b: 2 }]);
  });
});
