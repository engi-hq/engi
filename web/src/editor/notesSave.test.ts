import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { saveWorkflow } from '../api/hooks';
import type { WorkflowDoc } from '../api/types';
import { addNote, createNote, setNoteBoxes, updateNote } from './notes';

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe('notes persist through save', () => {
  it('sends the notes in the PUT body and keeps what the server returns', async () => {
    let doc: WorkflowDoc = { id: 'wf_1', name: 'w', version: 1, nodes: [], connections: [] };
    doc = addNote(doc, createNote(doc, [5, 6], 'nt_1'));
    doc = updateNote(doc, 'nt_1', { text: '**hi**', color: 'green' });
    doc = setNoteBoxes(doc, { nt_1: { position: [5, 6], size: { width: 320, height: 200 } } });

    // The server echoes the stored document with the next version.
    fetchMock.mockImplementation((_url: string, init: RequestInit) =>
      Promise.resolve(new Response(JSON.stringify({ ...JSON.parse(init.body as string), version: 2 }), { headers: { 'Content-Type': 'application/json' } })));
    const stored = await saveWorkflow(doc);

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/workflows/wf_1');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body as string).notes).toEqual([
      { id: 'nt_1', text: '**hi**', position: [5, 6], size: { width: 320, height: 200 }, color: 'green' },
    ]);
    expect(stored.version).toBe(2);
    expect(stored.doc.notes).toEqual(doc.notes);
  });

  it('loads a document without notes as before', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 'wf_1', name: 'w', version: 2, nodes: [], connections: [] }), { headers: { 'Content-Type': 'application/json' } }));
    const stored = await saveWorkflow({ id: 'wf_1', name: 'w', version: 1, nodes: [], connections: [] });
    expect(stored.doc.notes).toBeUndefined();
  });
});
