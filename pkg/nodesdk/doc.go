// Package nodesdk is the public, versioned contract between Engi and the
// nodes (integrations) that run inside it.
//
// A node implements Node: it describes itself with a Descriptor and executes
// against an Input, returning an Output. Nodes depend only on this package,
// never on engine internals. The package imports the standard library only.
package nodesdk
