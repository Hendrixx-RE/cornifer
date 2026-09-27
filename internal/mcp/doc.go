// Package mcp implements the MCP tool handlers: search_code,
// find_definition, find_references, get_dependencies, get_call_graph,
// get_blast_radius, and find_cycles (see plan.md "MCP server"). Each
// handler is a thin adapter that validates its JSON input, calls into
// internal/retrieve and/or internal/graph, and shapes a bounded (truncated,
// depth/limit-capped) JSON response. It depends on
// github.com/modelcontextprotocol/go-sdk for the tool/schema types;
// cmd/cornifer-mcp is the only thing that constructs the MCP server and
// registers these handlers on it.
package mcp
