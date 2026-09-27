// Package model defines the domain types shared by every other package in
// Cornifer: Repo, File, Symbol, Edge, Chunk, UnresolvedRef, and the enums
// that constrain their fields.
//
// This package is the contract layer. It has no behavior beyond small,
// pure helpers (Valid methods on enums) and must never import any other
// internal/ package — every other package imports model, never the reverse.
// Treat field names, types, and the line-range/enum semantics documented on
// each type as stable: parse, symbols, resolve, chunk, embed, store, bm25,
// retrieve, mcp, and eval all read or write these shapes, so a change here
// ripples everywhere.
package model
