import type { WorkflowDoc } from './types';

/**
 * The server omits zero values (`index: 0`, empty `port`), so documents are normalized
 * on the way in: every connection endpoint carries an explicit port kind and index.
 */
export function normalizeDoc(doc: WorkflowDoc): WorkflowDoc {
  return {
    ...doc,
    nodes: doc.nodes ?? [],
    connections: (doc.connections ?? []).map((c) => ({
      from: { ...c.from, port: c.from.port || 'main', index: c.from.index ?? 0 },
      to: { ...c.to, port: c.to.port || 'main', index: c.to.index ?? 0 },
    })),
  };
}
