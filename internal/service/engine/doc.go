// Package engine executes a workflow: a deterministic, depth-first scheduler
// over a ready stack and a waiting table, with retries, error routing,
// pause/resume and partial runs. It performs no I/O of its own; everything
// external goes through the interfaces in Deps.
package engine
