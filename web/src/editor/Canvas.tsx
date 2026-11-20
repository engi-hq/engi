import {
  applyEdgeChanges, applyNodeChanges, Background, BackgroundVariant, Controls, MarkerType, MiniMap, ReactFlow,
  type Connection as RFConnection, type Edge, type EdgeChange, type NodeChange,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useCallback, useEffect, useMemo, useState } from 'react';
import type { WfNote, WorkflowDoc } from '../api/types';
import { t, tn } from '../i18n/t';
import { groupVar, NodeCard, type CardNode } from './NodeCard';
import { NoteCard, type NoteNode } from './NoteCard';
import { isNodeRfId, isNoteRfId, nodeNameOf, nodeRfId, noteIdOf, noteRfId } from './canvasIds';
import { asNoteColor, removeNotes, setNoteBoxes, updateNote, type NoteBox } from './notes';
import {
  addConnection, checkConnection, descriptorFor, flatOf, removeConnection, removeNodes, setNodePositions, type ConnectionProblem, type TypeIndex,
} from './model';
import type { RunStates } from './runState';

const nodeTypes = { engi: NodeCard, note: NoteCard };

type CanvasNode = CardNode | NoteNode;
/** Sticky notes sit behind every node and edge. */
const NOTE_Z_INDEX = -1;

export type Apply = (fn: (d: WorkflowDoc) => WorkflowDoc, key?: string) => void;

interface Props {
  doc: WorkflowDoc;
  types: TypeIndex;
  states: RunStates;
  apply: Apply;
  readOnly: boolean;
  selected: string[];
  /** Ids of the selected sticky notes. */
  selectedNotes: string[];
  /** Largest note text the server accepts, in UTF-8 bytes. */
  noteTextLimit: number;
  connectFrom: string | null;
  onSelect: (names: string[]) => void;
  onSelectNotes: (ids: string[]) => void;
  onOpen: (name: string) => void;
  onRefused: (problem: ConnectionProblem) => void;
}

const parseHandle = (h: string | null | undefined): number => Number((h ?? '').split('-')[1] ?? 0);
const endpoints = (c: RFConnection | Edge) => (
  { fromNode: nodeNameOf(c.source), fromIndex: parseHandle(c.sourceHandle), toNode: nodeNameOf(c.target), toIndex: parseHandle(c.targetHandle) }
);
const edgeId = (c: WorkflowDoc['connections'][number]) => `${c.from.node}:${c.from.port ?? 'main'}:${c.from.index}>${c.to.node}:${c.to.index}`;

/** React Flow canvas bound to the workflow document. The document stays the single source of truth. */
export function Canvas({ doc, types, states, apply, readOnly, selected, selectedNotes, noteTextLimit, connectFrom, onSelect, onSelectNotes, onOpen, onRefused }: Props) {
  const [nodes, setNodes] = useState<CanvasNode[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);

  const patchNote = useCallback((id: string, patch: Partial<Omit<WfNote, 'id'>>, key?: string) => apply((d) => updateNote(d, id, patch), key), [apply]);
  const boxNote = useCallback((id: string, box: NoteBox) => apply((d) => setNoteBoxes(d, { [id]: box })), [apply]);
  const removeNote = useCallback((id: string) => { apply((d) => removeNotes(d, [id])); onSelectNotes(selectedNotes.filter((x) => x !== id)); }, [apply, onSelectNotes, selectedNotes]);

  // Rebuild React Flow's state from the document, keeping measured sizes and selection.
  useEffect(() => {
    setNodes((prev) => {
      const old = new Map(prev.map((n) => [n.id, n]));
      const cards = doc.nodes.map<CanvasNode>((n) => {
        const o = old.get(nodeRfId(n.name));
        return {
          id: nodeRfId(n.name),
          type: 'engi',
          position: { x: n.position?.[0] ?? 0, y: n.position?.[1] ?? 0 },
          data: { node: n, nt: descriptorFor(types, n), state: states[n.name], connectSource: connectFrom === n.name, pinned: !!doc.pinData?.[n.name] },
          selected: selected.includes(n.name),
          measured: o?.measured,
          draggable: !readOnly,
          connectable: !readOnly,
          deletable: !readOnly,
          ariaLabel: n.name,
        };
      });
      const stickies = (doc.notes ?? []).map<CanvasNode>((note) => {
        const o = old.get(noteRfId(note.id));
        return {
          id: noteRfId(note.id),
          type: 'note',
          position: { x: note.position[0], y: note.position[1] },
          width: note.size.width,
          height: note.size.height,
          zIndex: NOTE_Z_INDEX,
          data: { note, readOnly, maxTextBytes: noteTextLimit, onPatch: patchNote, onBox: boxNote, onRemove: removeNote },
          selected: selectedNotes.includes(note.id),
          measured: o?.measured,
          draggable: !readOnly,
          connectable: false,
          deletable: !readOnly,
          ariaLabel: t('note.label'),
        };
      });
      return [...stickies, ...cards];
    });
  }, [doc.nodes, doc.notes, doc.pinData, types, states, selected, selectedNotes, readOnly, connectFrom, noteTextLimit, patchNote, boxNote, removeNote]);

  useEffect(() => {
    setEdges((prev) => {
      const sel = new Set(prev.filter((e) => e.selected).map((e) => e.id));
      const descOf = (name: string) => {
        const n = doc.nodes.find((x) => x.name === name);
        return n ? descriptorFor(types, n) : undefined;
      };
      return doc.connections.map<Edge>((c) => {
        const kind = c.from.port ?? 'main';
        const count = kind === 'main' ? states[c.from.node]?.outputItems?.[c.from.index] : undefined;
        // Handle ids are positions in the descriptor's port lists; connections store the index among ports of their kind.
        const outFlat = flatOf(descOf(c.from.node)?.outputs ?? [], kind, c.from.index);
        const inFlat = flatOf(descOf(c.to.node)?.inputs ?? [], kind, c.to.index);
        const stateOfSrc = states[c.from.node]?.status;
        return {
          id: edgeId(c),
          source: nodeRfId(c.from.node),
          target: nodeRfId(c.to.node),
          sourceHandle: `out-${Math.max(outFlat, 0)}`,
          targetHandle: `in-${Math.max(inFlat, 0)}`,
          selected: sel.has(edgeId(c)),
          label: count !== undefined && stateOfSrc === 'success' ? tn('run.items', count) : undefined,
          animated: stateOfSrc === 'running',
          deletable: !readOnly,
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
          className: kind !== 'main' ? 'edge-sub' : undefined,
        };
      });
    });
  }, [doc.connections, doc.nodes, types, states, readOnly]);

  const onNodesChange = useCallback((changes: NodeChange<CanvasNode>[]) => {
    setNodes((ns) => applyNodeChanges(changes, ns));
    const sel = changes.filter((c) => c.type === 'select');
    if (sel.length > 0) {
      // Derive the new selection from the changes applied to the current one.
      const next = new Set(selected);
      const nextNotes = new Set(selectedNotes);
      for (const c of sel) {
        if (c.type !== 'select') continue;
        const set = isNoteRfId(c.id) ? nextNotes : next;
        const id = isNoteRfId(c.id) ? noteIdOf(c.id) : nodeNameOf(c.id);
        if (c.selected) set.add(id);
        else set.delete(id);
      }
      onSelect([...next]);
      onSelectNotes([...nextNotes]);
    }
  }, [selected, selectedNotes, onSelect, onSelectNotes]);

  const onEdgesChange = useCallback((changes: EdgeChange[]) => setEdges((es) => applyEdgeChanges(changes, es)), []);

  // Refuses a drop on a port of another kind, a full input or an existing link while dragging, not only after.
  const isValidConnection = useCallback((c: RFConnection | Edge) => checkConnection(doc, types, endpoints(c)) === null, [doc, types]);

  const onConnect = useCallback((c: RFConnection) => {
    const e = endpoints(c);
    apply((d) => {
      const problem = checkConnection(d, types, e);
      if (problem) {
        queueMicrotask(() => onRefused(problem));
        return d;
      }
      return addConnection(d, types, e);
    });
  }, [apply, types, onRefused]);

  const focusNeighbour = (e: React.KeyboardEvent) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-node]');
    if (!el) return;
    const name = el.dataset['node'] as string;
    const pick = (cands: string[]) => cands[0];
    let next: string | undefined;
    if (e.key === 'ArrowRight') next = pick(doc.connections.filter((c) => c.from.node === name).map((c) => c.to.node));
    else if (e.key === 'ArrowLeft') next = pick(doc.connections.filter((c) => c.to.node === name).map((c) => c.from.node));
    else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const parents = doc.connections.filter((c) => c.to.node === name).map((c) => c.from.node);
      const sibs = [...new Set(doc.connections.filter((c) => parents.includes(c.from.node)).map((c) => c.to.node))];
      const i = sibs.indexOf(name);
      next = sibs[i + (e.key === 'ArrowDown' ? 1 : -1)];
    } else if (e.key === 'Enter') {
      onOpen(name);
      return;
    } else return;
    if (!next) return;
    e.preventDefault();
    onSelect([next]);
    document.querySelector<HTMLElement>(`.react-flow__node[data-id="${CSS.escape(nodeRfId(next))}"]`)?.focus();
  };

  const miniColor = useMemo(() => (n: { data?: unknown }) => {
    const d = n.data as Partial<CardNode['data'] & NoteNode['data']> | undefined;
    return d?.note ? `var(--note-${asNoteColor(d.note.color)}-border)` : groupVar(d?.nt?.group);
  }, []);

  return (
    <div className="engi-flow" onKeyDown={focusNeighbour}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        isValidConnection={isValidConnection}
        onNodeDragStop={(_, __, all) => {
          if (readOnly) return;
          const pos: Record<string, [number, number]> = {};
          const boxes: Record<string, NoteBox> = {};
          for (const n of all) {
            if (isNoteRfId(n.id)) boxes[noteIdOf(n.id)] = { position: [n.position.x, n.position.y] };
            else pos[nodeNameOf(n.id)] = [Math.round(n.position.x), Math.round(n.position.y)];
          }
          apply((d) => setNoteBoxes(setNodePositions(d, pos), boxes), 'move');
        }}
        onNodesDelete={(ns) => {
          const noteIds = ns.filter((n) => isNoteRfId(n.id)).map((n) => noteIdOf(n.id));
          apply((d) => removeNotes(removeNodes(d, ns.filter((n) => isNodeRfId(n.id)).map((n) => nodeNameOf(n.id))), noteIds));
          if (noteIds.length > 0) onSelectNotes(selectedNotes.filter((id) => !noteIds.includes(id)));
        }}
        onEdgesDelete={(es) => apply((d) => es.reduce((acc, e) => {
          const c = acc.connections.find((x) => edgeId(x) === e.id);
          return c ? removeConnection(acc, c) : acc;
        }, d))}
        onNodeDoubleClick={(_, n) => { if (isNodeRfId(n.id)) onOpen(nodeNameOf(n.id)); }}
        onPaneClick={() => { onSelect([]); onSelectNotes([]); }}
        elevateNodesOnSelect={false}
        nodesConnectable={!readOnly}
        elementsSelectable
        deleteKeyCode={readOnly ? null : ['Delete', 'Backspace']}
        selectionOnDrag={false}
        multiSelectionKeyCode={['Meta', 'Control', 'Shift']}
        fitView
        fitViewOptions={{ padding: 0.3, maxZoom: 1 }}
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
        aria-label={t('canvas.label')}
        defaultEdgeOptions={{ type: 'default' }}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1.5} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeColor={miniColor} ariaLabel={t('canvas.minimap')} />
      </ReactFlow>
    </div>
  );
}
