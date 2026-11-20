// Package mcp implements the parts of the Model Context Protocol (MCP) that
// Engi needs, using only the standard library: the JSON-RPC 2.0 message types,
// a client for MCP servers (Streamable HTTP and the legacy HTTP+SSE transport),
// and a transport-independent server dispatcher that the MCP server trigger's
// HTTP handler drives.
//
// The package is a neutral leaf: it depends on nothing else in Engi, so nodes
// (the MCP client tool), services and adapters can all import it. It does not
// read configuration; callers pass limits in.
package mcp
