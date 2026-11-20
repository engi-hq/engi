import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { WfNote } from '../api/types';
import { NoteView } from './NoteCard';

const note = (over: Partial<WfNote> = {}): WfNote => ({ id: 'nt_1', text: 'Hello **world**', position: [0, 0], size: { width: 240, height: 160 }, color: 'blue', ...over });

function setup(props: { note?: WfNote; selected?: boolean; readOnly?: boolean; maxTextBytes?: number } = {}) {
  const onPatch = vi.fn();
  const onRemove = vi.fn();
  const view = render(
    <NoteView
      note={props.note ?? note()} selected={props.selected ?? false} readOnly={props.readOnly ?? false}
      maxTextBytes={props.maxTextBytes ?? 16384} onPatch={onPatch} onRemove={onRemove}
    />,
  );
  return { onPatch, onRemove, ...view };
}

describe('NoteView', () => {
  it('renders markdown and uses the colour tokens', () => {
    setup();
    expect(screen.getByText('world').tagName).toBe('STRONG');
    const el = screen.getByRole('note');
    expect(el).toHaveAttribute('data-color', 'blue');
    expect(el.style.background).toBe('var(--note-blue-bg)');
  });

  it('keeps script tags and javascript links inert', () => {
    const { container } = setup({ note: note({ text: '<script>1</script>\n\n[x](javascript:alert)' }) });
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('a')).toBeNull();
  });

  it('edits on double-click and saves the text when focus leaves', async () => {
    const { onPatch } = setup();
    await userEvent.dblClick(screen.getByText('world'));
    const box = screen.getByRole('textbox', { name: 'Note text (markdown)' });
    expect(box).toHaveFocus();
    expect(box).toHaveValue('Hello **world**');
    await userEvent.clear(box);
    await userEvent.type(box, '# New');
    await userEvent.tab();
    expect(onPatch).toHaveBeenCalledWith('nt_1', { text: '# New' });
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('saves on Escape and does not patch when the text is unchanged', async () => {
    const { onPatch } = setup();
    await userEvent.dblClick(screen.getByText('world'));
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(onPatch).not.toHaveBeenCalled();
  });

  it('shows a prompt for an empty note and edits it', async () => {
    const { onPatch } = setup({ note: note({ text: '' }) });
    await userEvent.dblClick(screen.getByText('Double-click to write a note'));
    await userEvent.type(screen.getByRole('textbox'), 'first');
    await userEvent.tab();
    expect(onPatch).toHaveBeenCalledWith('nt_1', { text: 'first' });
  });

  it('changes colour and deletes from the toolbar of a selected note', async () => {
    const { onPatch, onRemove } = setup({ selected: true });
    expect(screen.getByRole('button', { name: 'Colour: Blue' })).toHaveAttribute('aria-pressed', 'true');
    await userEvent.click(screen.getByRole('button', { name: 'Colour: Green' }));
    expect(onPatch).toHaveBeenCalledWith('nt_1', { color: 'green' });
    await userEvent.click(screen.getByRole('button', { name: 'Delete note' }));
    expect(onRemove).toHaveBeenCalledWith('nt_1');
  });

  it('has no toolbar unless selected and is not editable when read-only', async () => {
    setup();
    expect(screen.queryByRole('toolbar')).toBeNull();
    const ro = setup({ selected: true, readOnly: true });
    expect(ro.container.querySelector('[role=toolbar]')).toBeNull();
    await userEvent.dblClick(ro.container.querySelector('.note-body') as HTMLElement);
    expect(ro.container.querySelector('textarea')).toBeNull();
  });

  it('counts the text in UTF-8 bytes while editing', async () => {
    setup({ note: note({ text: 'ab' }), maxTextBytes: 10 });
    await userEvent.dblClick(screen.getByText('ab'));
    expect(screen.getByText('2 / 10 bytes')).toBeInTheDocument();
    await userEvent.type(screen.getByRole('textbox'), 'é');
    expect(screen.getByText('4 / 10 bytes')).toBeInTheDocument();
  });

  it('refuses input that would pass the limit and saves what fits', async () => {
    const { onPatch } = setup({ note: note({ text: 'ab' }), maxTextBytes: 4 });
    await userEvent.dblClick(screen.getByText('ab'));
    const box = screen.getByRole('textbox');
    await userEvent.type(box, 'é界'); // 2 + 2 + 3 bytes: the first character fits, the second does not
    expect(box).toHaveValue('abé');
    expect(screen.getByText('4 / 4 bytes')).not.toHaveAttribute('data-over');
    await userEvent.tab();
    expect(onPatch).toHaveBeenCalledWith('nt_1', { text: 'abé' });
  });

  it('lets a text that is already over the limit be cut down', async () => {
    const { onPatch } = setup({ note: note({ text: 'abcdef' }), maxTextBytes: 3 });
    await userEvent.dblClick(screen.getByText('abcdef'));
    const box = screen.getByRole('textbox');
    expect(screen.getByText('6 / 3 bytes')).toHaveAttribute('data-over');
    await userEvent.type(box, 'x');
    expect(box).toHaveValue('abcdef');
    await userEvent.clear(box);
    await userEvent.type(box, 'ab');
    await userEvent.tab();
    expect(onPatch).toHaveBeenCalledWith('nt_1', { text: 'ab' });
  });
});
