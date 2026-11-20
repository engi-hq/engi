import type { WfNode, WfNote, WorkflowDoc } from '../api/types';

/** Spacing for auto-layout, in canvas pixels. The single source for every layout number. */
export const LAYOUT_CONFIG = {
  columnWidth: 280,
  rowHeight: 120,
  /** Weight of an output index when ordering siblings of one parent (keeps port order, must stay below 1/maxPorts). */
  portWeight: 0.01,
  /** Disconnected nodes wrap into rows of this many. */
  strayPerRow: 6,
  /** Extra vertical gap between the connected graph and the disconnected nodes, in rows. */
  strayGapRows: 1,
  fitPadding: 0.3,
  fitMaxZoom: 1,
  /**
   * Footprint of a node card, used only to decide which sticky notes sit over a node. Cards are 190 px wide;
   * their height grows with the ports, so the minimum height stands in for it.
   */
  nodeWidth: 190,
  nodeHeight: 64,
} as const;

type Pos = [number, number];
const posOf = (n: WfNode): Pos => n.position ?? [0, 0];
const byPosition = (a: WfNode, b: WfNode): number => posOf(a)[1] - posOf(b)[1] || posOf(a)[0] - posOf(b)[0] || a.name.localeCompare(b.name);

interface Edge { from: string; to: string; out: number }

interface Box { x: number; y: number; w: number; h: number }

const overlapArea = (a: Box, b: Box): number => {
  const w = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
  const h = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
  return w > 0 && h > 0 ? w * h : 0;
};

/**
 * Notes move with the node they overlap most, by the same distance, so they keep their place on the graph.
 * A note over no node (or over a node that stays put) keeps its position. Returns undefined when no note moves.
 */
function carryNotes(doc: WorkflowDoc, next: Map<string, Pos>): WfNote[] | undefined {
  const notes = doc.notes;
  if (!notes || notes.length === 0) return undefined;
  const cards = doc.nodes.map((n) => {
    const from = posOf(n);
    const to = next.get(n.name) ?? from;
    return { box: { x: from[0], y: from[1], w: LAYOUT_CONFIG.nodeWidth, h: LAYOUT_CONFIG.nodeHeight }, delta: [to[0] - from[0], to[1] - from[1]] as Pos };
  });
  let changed = false;
  const out = notes.map((note) => {
    const box: Box = { x: note.position[0], y: note.position[1], w: note.size.width, h: note.size.height };
    let anchor: (typeof cards)[number] | undefined;
    let best = 0;
    for (const card of cards) {
      const area = overlapArea(box, card.box);
      if (area > best) { best = area; anchor = card; }
    }
    if (!anchor || (anchor.delta[0] === 0 && anchor.delta[1] === 0)) return note;
    changed = true;
    return { ...note, position: [note.position[0] + anchor.delta[0], note.position[1] + anchor.delta[1]] as Pos };
  });
  return changed ? out : undefined;
}

/**
 * Left-to-right layered layout (longest-path layering, then barycenter ordering).
 *
 * Only `names` are moved when given with 2+ entries (anchored at their top-left corner); otherwise the whole workflow.
 * Ordering rules, which matter because the engine runs sibling branches in canvas top-to-bottom order:
 * siblings are stacked by output port first (IF true above false), and siblings on the same port keep their existing vertical order.
 * Back edges (loops) are ignored for layering, so cycles cannot recurse forever.
 * Sticky notes: a note moves with the node it overlaps most (see `carryNotes`); notes over no node stay where they are.
 */
export function autoLayout(doc: WorkflowDoc, names?: readonly string[]): WorkflowDoc {
  const only = names && names.length >= 2 ? new Set(names) : undefined;
  const nodes = doc.nodes.filter((n) => !only || only.has(n.name)).sort(byPosition);
  if (nodes.length === 0) return doc;
  const inSet = new Set(nodes.map((n) => n.name));

  const seen = new Set<string>();
  const edges: Edge[] = [];
  for (const c of doc.connections) {
    const key = `${c.from.node}\0${c.from.port}\0${c.from.index}\0${c.to.node}`;
    if (!inSet.has(c.from.node) || !inSet.has(c.to.node) || c.from.node === c.to.node || seen.has(key)) continue;
    seen.add(key);
    edges.push({ from: c.from.node, to: c.to.node, out: c.from.index });
  }
  edges.sort((a, b) => a.out - b.out);

  const outs = new Map<string, Edge[]>();
  const ins = new Map<string, Edge[]>();
  for (const e of edges) {
    outs.set(e.from, [...(outs.get(e.from) ?? []), e]);
    ins.set(e.to, [...(ins.get(e.to) ?? []), e]);
  }
  const connected = nodes.filter((n) => outs.has(n.name) || ins.has(n.name));
  const stray = nodes.filter((n) => !outs.has(n.name) && !ins.has(n.name));

  // DFS from roots (no inputs) first, then anything left (pure cycles); edges into the DFS stack are back edges.
  const state = new Map<string, 1 | 2>();
  const order: string[] = [];
  const back = new Set<Edge>();
  const visit = (name: string): void => {
    state.set(name, 1);
    for (const e of outs.get(name) ?? []) {
      const s = state.get(e.to);
      if (s === 1) back.add(e);
      else if (s === undefined) visit(e.to);
    }
    state.set(name, 2);
    order.push(name);
  };
  const roots = connected.filter((n) => !ins.has(n.name));
  for (const n of [...roots, ...connected]) if (!state.has(n.name)) visit(n.name);

  const layer = new Map<string, number>();
  for (const name of [...order].reverse()) {
    let l = 0;
    for (const e of ins.get(name) ?? []) if (!back.has(e)) l = Math.max(l, (layer.get(e.from) ?? 0) + 1);
    layer.set(name, l);
  }

  // Order each layer left to right by the mean (rank of parent + port offset) of its parents.
  const layers: string[][] = [];
  const rank = new Map<string, number>();
  const idx = new Map(connected.map((n, i) => [n.name, i]));
  const maxLayer = Math.max(-1, ...layer.values());
  for (let l = 0; l <= maxLayer; l++) {
    const members = connected.filter((n) => layer.get(n.name) === l).map((n) => n.name);
    const key = (name: string): number => {
      const ps = (ins.get(name) ?? []).filter((e) => !back.has(e) && rank.has(e.from));
      if (ps.length === 0) return 0;
      return ps.reduce((s, e) => s + (rank.get(e.from) ?? 0) + e.out * LAYOUT_CONFIG.portWeight, 0) / ps.length;
    };
    members.sort((a, b) => key(a) - key(b) || (idx.get(a) ?? 0) - (idx.get(b) ?? 0));
    members.forEach((m, i) => rank.set(m, i));
    layers.push(members);
  }

  const all = nodes.map(posOf);
  const ox = Math.min(...all.map((p) => p[0]));
  const oy = Math.min(...all.map((p) => p[1]));
  const next = new Map<string, Pos>();
  layers.forEach((members, l) => members.forEach((m, i) => next.set(m, [ox + l * LAYOUT_CONFIG.columnWidth, oy + i * LAYOUT_CONFIG.rowHeight])));
  const rows = Math.max(0, ...layers.map((m) => m.length));
  const strayTop = oy + (rows > 0 ? rows + LAYOUT_CONFIG.strayGapRows : 0) * LAYOUT_CONFIG.rowHeight;
  stray.forEach((n, i) => {
    next.set(n.name, [
      ox + (i % LAYOUT_CONFIG.strayPerRow) * LAYOUT_CONFIG.columnWidth,
      strayTop + Math.floor(i / LAYOUT_CONFIG.strayPerRow) * LAYOUT_CONFIG.rowHeight,
    ]);
  });

  let changed = false;
  const out = doc.nodes.map((n) => {
    const p = next.get(n.name);
    if (!p) return n;
    const cur = posOf(n);
    if (cur[0] === p[0] && cur[1] === p[1]) return n;
    changed = true;
    return { ...n, position: p };
  });
  const notes = carryNotes(doc, next);
  if (!changed && !notes) return doc;
  return { ...doc, nodes: out, ...(notes ? { notes } : {}) };
}
