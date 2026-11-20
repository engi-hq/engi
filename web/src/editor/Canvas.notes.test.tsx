import { ReactFlowProvider } from '@xyflow/react';
import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { WorkflowDoc } from '../api/types';
import { Canvas, type Apply } from './Canvas';
import { commit, initHistory } from './history';
import { addNote, createNote } from './notes';

beforeAll(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 });
});

function Harness({ start, onDoc }: { start: WorkflowDoc; onDoc: (d: WorkflowDoc) => void }) {
  const [hist, setHist] = useState(() => initHistory(start));
  const [notes, setNotes] = useState<string[]>([]);
  const apply: Apply = (fn, key) => setHist((h) => { const next = commit(h, fn(h.present), key); onDoc(next.present); return next; });
  return (
    <div style={{ width: 800, height: 600 }}>
      <Canvas
        doc={hist.present} types={new Map()} states={{}} apply={apply} readOnly={false}
        selected={[]} selectedNotes={notes} noteTextLimit={16384} connectFrom={null}
        onSelect={() => {}} onSelectNotes={setNotes} onOpen={() => {}} onRefused={() => {}}
      />
    </div>
  );
}

describe('Canvas with sticky notes', () => {
  const base: WorkflowDoc = { name: 'w', nodes: [], connections: [] };

  it('renders notes from the document and edits their text on double-click', async () => {
    const start = addNote(base, { ...createNote(base, [0, 0], 'nt_1'), text: 'Remember **this**' });
    let latest = start;
    render(<ReactFlowProvider><Harness start={start} onDoc={(d) => { latest = d; }} /></ReactFlowProvider>);
    expect(screen.getByText('this').tagName).toBe('STRONG');

    // d3-drag (React Flow's pointer handling) cannot take jsdom's synthetic mouse-down, so use plain events.
    fireEvent.doubleClick(screen.getByText('this'));
    const box = screen.getByRole('textbox', { name: 'Note text (markdown)' });
    fireEvent.change(box, { target: { value: 'changed' } });
    fireEvent.blur(box);
    expect(latest.notes?.[0]?.text).toBe('changed');
  });

  it('deletes a selected note from its toolbar', async () => {
    const start = addNote(base, createNote(base, [0, 0], 'nt_1'));
    let latest = start;
    render(<ReactFlowProvider><Harness start={start} onDoc={(d) => { latest = d; }} /></ReactFlowProvider>);
    fireEvent.click(screen.getByRole('note'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete note' }));
    expect(latest.notes).toBeUndefined();
    expect(screen.queryByRole('note')).toBeNull();
  });
});

describe('Canvas ids', () => {
  it('keeps a node named like a note apart from that note', () => {
    const base: WorkflowDoc = { name: 'w', nodes: [{ name: 'note:nt_1', type: 't', position: [0, 0] }], connections: [] };
    const start = addNote(base, { ...createNote(base, [300, 0], 'nt_1'), text: 'the note' });
    render(<ReactFlowProvider><Harness start={start} onDoc={() => {}} /></ReactFlowProvider>);
    expect(document.querySelectorAll('[data-node="note:nt_1"]')).toHaveLength(1);
    expect(screen.getByText('the note')).toBeInTheDocument();
    expect(screen.getAllByRole('note')).toHaveLength(1);
  });
});
