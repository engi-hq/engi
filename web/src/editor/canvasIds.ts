// React Flow ids of the canvas. Nodes and sticky notes share one id space, and a node name is free text
// (it may even look like a note id), so each kind gets its own prefix. Neither prefix is a prefix of the
// other, so no node name can produce a note id, whatever the name contains.

const NODE_PREFIX = 'node:';
const NOTE_PREFIX = 'note:';

/** React Flow id of a workflow node, from its name. */
export const nodeRfId = (name: string): string => NODE_PREFIX + name;
export const isNodeRfId = (rfId: string): boolean => rfId.startsWith(NODE_PREFIX);
/** The node name behind a React Flow node id. */
export const nodeNameOf = (rfId: string): string => rfId.slice(NODE_PREFIX.length);

/** React Flow id of a sticky note, from its id. */
export const noteRfId = (id: string): string => NOTE_PREFIX + id;
export const isNoteRfId = (rfId: string): boolean => rfId.startsWith(NOTE_PREFIX);
/** The note id behind a React Flow node id. */
export const noteIdOf = (rfId: string): string => rfId.slice(NOTE_PREFIX.length);
