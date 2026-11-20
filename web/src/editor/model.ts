import type { Connection, NodeSettings, NodeType, PortSpec, WfNode, WfNote, WorkflowDoc } from '../api/types';
import { normalizeDoc } from '../api/normalize';
import { defaultParameters } from '../forms/logic';
import { cloneNotes, notesOf, sanitizeNotes } from './notes';

export type TypeIndex = Map<string, NodeType>;

/** Index node types by `type`; the newest version wins, older ones stay reachable by `type@version`. */
export function indexTypes(types: NodeType[]): TypeIndex {
  const idx: TypeIndex = new Map();
  for (const nt of types) {
    idx.set(`${nt.type}@${nt.version}`, nt);
    const cur = idx.get(nt.type);
    if (!cur || cur.version < nt.version) idx.set(nt.type, nt);
  }
  return idx;
}

export function descriptorFor(idx: TypeIndex, node: Pick<WfNode, 'type' | 'typeVersion'>): NodeType | undefined {
  return (node.typeVersion ? idx.get(`${node.type}@${node.typeVersion}`) : undefined) ?? idx.get(node.type);
}

export function uniqueName(base: string, taken: Iterable<string>): string {
  const names = new Set(taken);
  if (!names.has(base)) return base;
  const stem = base.replace(/ \d+$/, '');
  for (let i = 1; ; i++) {
    const cand = `${stem} ${i}`;
    if (!names.has(cand)) return cand;
  }
}

export const nodeNames = (doc: WorkflowDoc): string[] => doc.nodes.map((n) => n.name);

export function createNode(nt: NodeType, doc: WorkflowDoc, position: [number, number]): WfNode {
  return {
    name: uniqueName(nt.displayName, nodeNames(doc)),
    type: nt.type,
    typeVersion: nt.version,
    position,
    parameters: defaultParameters(nt.properties),
  };
}

export function addNode(doc: WorkflowDoc, node: WfNode): WorkflowDoc {
  return { ...doc, nodes: [...doc.nodes, node] };
}

export function updateNode(doc: WorkflowDoc, name: string, patch: Partial<WfNode>): WorkflowDoc {
  return { ...doc, nodes: doc.nodes.map((n) => (n.name === name ? { ...n, ...patch } : n)) };
}

export function removeNodes(doc: WorkflowDoc, names: string[]): WorkflowDoc {
  const gone = new Set(names);
  const pin = doc.pinData ? Object.fromEntries(Object.entries(doc.pinData).filter(([k]) => !gone.has(k))) : undefined;
  return {
    ...doc,
    nodes: doc.nodes.filter((n) => !gone.has(n.name)),
    connections: doc.connections.filter((c) => !gone.has(c.from.node) && !gone.has(c.to.node)),
    pinData: pin,
  };
}

export type RenameResult = { ok: true; doc: WorkflowDoc } | { ok: false; reason: 'empty' | 'duplicate' };

export function renameNode(doc: WorkflowDoc, from: string, to: string): RenameResult {
  const name = to.trim();
  if (name === '') return { ok: false, reason: 'empty' };
  if (name === from) return { ok: true, doc };
  if (doc.nodes.some((n) => n.name === name)) return { ok: false, reason: 'duplicate' };
  const re = (c: Connection): Connection => ({
    from: c.from.node === from ? { ...c.from, node: name } : c.from,
    to: c.to.node === from ? { ...c.to, node: name } : c.to,
  });
  let pinData = doc.pinData;
  if (pinData && from in pinData) {
    pinData = Object.fromEntries(Object.entries(pinData).map(([k, v]) => [k === from ? name : k, v]));
  }
  return {
    ok: true,
    doc: { ...doc, nodes: doc.nodes.map((n) => (n.name === from ? { ...n, name } : n)), connections: doc.connections.map(re), pinData },
  };
}

export function setNodePositions(doc: WorkflowDoc, pos: Record<string, [number, number]>): WorkflowDoc {
  return { ...doc, nodes: doc.nodes.map((n) => (pos[n.name] ? { ...n, position: pos[n.name] } : n)) };
}

export function setSettings(doc: WorkflowDoc, name: string, settings: NodeSettings): WorkflowDoc {
  return updateNode(doc, name, { settings });
}

export interface Endpoints {
  fromNode: string;
  fromIndex: number;
  toNode: string;
  toIndex: number;
}

export type ConnectionProblem = 'self' | 'duplicate' | 'kind' | 'noOutput' | 'noInput' | 'unknownNode' | 'maxConnections';

/** Sub-ports (`ai.*`) carry a capability to a parent node instead of items. */
export const isSubKind = (kind: string | undefined): boolean => !!kind && kind !== 'main';

/** Position of port `flat` among the ports of its own kind: what a connection stores as `index`. */
export function ordinalOf(ports: PortSpec[], flat: number): number {
  const kind = ports[flat]?.kind;
  return ports.slice(0, flat).filter((p) => p.kind === kind).length;
}

/** Index in `ports` of the ordinal-th port of `kind`, or -1. */
export function flatOf(ports: PortSpec[], kind: string, ordinal: number): number {
  let seen = 0;
  for (let i = 0; i < ports.length; i++) {
    if (ports[i]?.kind !== kind) continue;
    if (seen === ordinal) return i;
    seen++;
  }
  return -1;
}

/** Why a connection is refused, or null when it is allowed. Cycles are allowed (loops). Indices are positions in the descriptor's port lists. */
export function checkConnection(doc: WorkflowDoc, idx: TypeIndex, e: Endpoints): ConnectionProblem | null {
  if (e.fromNode === e.toNode) return 'self';
  const a = doc.nodes.find((n) => n.name === e.fromNode);
  const b = doc.nodes.find((n) => n.name === e.toNode);
  if (!a || !b) return 'unknownNode';
  const da = descriptorFor(idx, a);
  const db = descriptorFor(idx, b);
  const out = da?.outputs[e.fromIndex];
  const inp = db?.inputs[e.toIndex];
  if (!out) return 'noOutput';
  if (!inp) return 'noInput';
  if (out.kind !== inp.kind) return 'kind';
  const fromOrd = ordinalOf(da.outputs, e.fromIndex);
  const toOrd = ordinalOf(db.inputs, e.toIndex);
  const into = doc.connections.filter((c) => c.to.node === e.toNode && (c.to.port ?? 'main') === inp.kind && c.to.index === toOrd);
  if (into.some((c) => c.from.node === e.fromNode && c.from.index === fromOrd)) return 'duplicate';
  if (isSubKind(inp.kind) && inp.maxConnections && into.length >= inp.maxConnections) return 'maxConnections';
  return null;
}

export function addConnection(doc: WorkflowDoc, idx: TypeIndex, e: Endpoints): WorkflowDoc {
  const from = descriptorFor(idx, doc.nodes.find((n) => n.name === e.fromNode) as WfNode);
  const to = descriptorFor(idx, doc.nodes.find((n) => n.name === e.toNode) as WfNode);
  const kind = from?.outputs[e.fromIndex]?.kind ?? 'main';
  const c: Connection = {
    from: { node: e.fromNode, port: kind, index: from ? ordinalOf(from.outputs, e.fromIndex) : e.fromIndex },
    to: { node: e.toNode, port: kind, index: to ? ordinalOf(to.inputs, e.toIndex) : e.toIndex },
  };
  return { ...doc, connections: [...doc.connections, c] };
}

export function removeConnection(doc: WorkflowDoc, c: Connection): WorkflowDoc {
  const same = (x: Connection) =>
    x.from.node === c.from.node && x.from.index === c.from.index && x.to.node === c.to.node && x.to.index === c.to.index &&
    (x.from.port ?? 'main') === (c.from.port ?? 'main');
  return { ...doc, connections: doc.connections.filter((x) => !same(x)) };
}

/** First free input-less trigger check: does this node accept incoming edges? */
export const isTrigger = (nt: NodeType | undefined): boolean => !!nt && nt.inputs.length === 0;

// ---- Copy / paste as workflow JSON ----

export interface Fragment {
  engi: 'fragment';
  nodes: WfNode[];
  connections: Connection[];
  /** Sticky notes travel with nodes. Absent in fragments from older copies. */
  notes?: WfNote[];
}

export function copyFragment(doc: WorkflowDoc, names: string[], noteIds: string[] = []): Fragment {
  const set = new Set(names);
  const noteSet = new Set(noteIds);
  const notes = notesOf(doc).filter((n) => noteSet.has(n.id));
  return {
    engi: 'fragment',
    nodes: doc.nodes.filter((n) => set.has(n.name)),
    connections: doc.connections.filter((c) => set.has(c.from.node) && set.has(c.to.node)),
    ...(notes.length > 0 ? { notes } : {}),
  };
}

export function parseFragment(text: string): Fragment | null {
  try {
    const v = JSON.parse(text) as Partial<Fragment & WorkflowDoc>;
    if (!v || typeof v !== 'object') return null;
    const notes = sanitizeNotes(v.notes);
    const rawNodes = Array.isArray(v.nodes) ? v.nodes : [];
    if (rawNodes.length === 0 && notes.length === 0) return null;
    if (!rawNodes.every((n) => n && typeof n.name === 'string' && typeof n.type === 'string')) return null;
    const { nodes, connections } = normalizeDoc({ name: '', nodes: rawNodes, connections: Array.isArray(v.connections) ? v.connections : [] });
    return { engi: 'fragment', nodes, connections, ...(notes.length > 0 ? { notes } : {}) };
  } catch {
    return null;
  }
}

export interface PasteResult {
  doc: WorkflowDoc;
  names: string[];
  /** Ids of the notes that were added. */
  noteIds: string[];
}

/** Adds the fragment with fresh ids, collision-free names and an offset. Credentials references are kept. */
export function pasteFragment(doc: WorkflowDoc, frag: Fragment, offset: [number, number] = [40, 40]): PasteResult {
  const taken = new Set(nodeNames(doc));
  const map = new Map<string, string>();
  const nodes: WfNode[] = frag.nodes.map((n) => {
    const name = uniqueName(n.name, taken);
    taken.add(name);
    map.set(n.name, name);
    const [x, y] = n.position ?? [0, 0];
    const { id: _drop, ...rest } = n;
    void _drop;
    return { ...rest, name, position: [x + offset[0], y + offset[1]] };
  });
  const connections = frag.connections
    .filter((c) => map.has(c.from.node) && map.has(c.to.node))
    .map<Connection>((c) => ({
      from: { ...c.from, node: map.get(c.from.node) as string },
      to: { ...c.to, node: map.get(c.to.node) as string },
    }));
  const notes = cloneNotes(frag.notes ?? [], offset);
  const merged = notes.length > 0 ? { notes: [...notesOf(doc), ...notes] } : {};
  return {
    doc: { ...doc, nodes: [...doc.nodes, ...nodes], connections: [...doc.connections, ...connections], ...merged },
    names: nodes.map((n) => n.name),
    noteIds: notes.map((n) => n.id),
  };
}

/** Where to put a new node: right of the node it follows, else near the origin of the viewport. */
export function nextPosition(doc: WorkflowDoc, after?: string, fallback: [number, number] = [0, 0]): [number, number] {
  const prev = after ? doc.nodes.find((n) => n.name === after) : undefined;
  if (prev?.position) return [prev.position[0] + 240, prev.position[1]];
  return fallback;
}

/** Output index a new edge from `name` should start at: the first unconnected output, else the first. -1 without outputs. */
export function firstFreeOutput(doc: WorkflowDoc, idx: TypeIndex, name: string): number {
  const n = doc.nodes.find((x) => x.name === name);
  const d = n ? descriptorFor(idx, n) : undefined;
  if (!d || d.outputs.length === 0) return -1;
  const used = new Set(doc.connections.filter((c) => c.from.node === name).map((c) => `${c.from.port ?? 'main'}/${c.from.index}`));
  const free = d.outputs.findIndex((p, i) => !used.has(`${p.kind}/${ordinalOf(d.outputs, i)}`));
  return free >= 0 ? free : 0;
}

const CARD_W = 200;
const CARD_H = 80;

/** Moves a position down until it no longer overlaps an existing node card. */
export function freePosition(doc: WorkflowDoc, pos: [number, number]): [number, number] {
  const x = pos[0];
  let y = pos[1];
  const hit = () => doc.nodes.some((n) => {
    const [nx, ny] = n.position ?? [0, 0];
    return Math.abs(nx - x) < CARD_W && Math.abs(ny - y) < CARD_H;
  });
  for (let i = 0; i < 50 && hit(); i++) y += CARD_H;
  return [x, y];
}

/** Pins (or, with undefined, unpins) a node's output items. */
export function setPin(doc: WorkflowDoc, node: string, items: object[] | undefined): WorkflowDoc {
  const pins = { ...(doc.pinData ?? {}) };
  if (items === undefined) delete pins[node]; else pins[node] = items;
  return { ...doc, pinData: Object.keys(pins).length ? pins : undefined };
}

/** Parses pinned-data JSON: it must be an array of plain objects. */
export function parsePinned(text: string): { ok: true; items: object[] } | { ok: false; reason: 'syntax' | 'array' | 'objects' } {
  let v: unknown;
  try { v = JSON.parse(text); } catch { return { ok: false, reason: 'syntax' }; }
  if (!Array.isArray(v)) return { ok: false, reason: 'array' };
  if (!v.every((x) => x !== null && typeof x === 'object' && !Array.isArray(x))) return { ok: false, reason: 'objects' };
  return { ok: true, items: v as object[] };
}
