package graph

import "github.com/Hendrixx-RE/cornifer/internal/model"

// defaultBlastRadiusKinds is the edge-kind set BlastRadius uses when
// opts.Kinds is empty: "what breaks if I change this?" is answered by
// walking imports (who depends on this file/module) and calls (who
// depends on this symbol's behavior) in reverse, per plan.md "Blast
// radius".
var defaultBlastRadiusKinds = []model.EdgeKind{model.EdgeKindImports, model.EdgeKindCalls}

// BlastRadius computes the reverse transitive closure of symbolID over
// import and call edges (or opts.Kinds, if non-empty): everything that
// would be affected by a change to symbolID, up to opts.MaxDepth hops away.
// It is cycle-safe (import cycles and mutual recursion never cause
// non-termination or duplicate results) and returns one Path per affected
// symbol so a caller can explain *why* that symbol is in the blast radius,
// not just that it is.
func BlastRadius(g *Graph, symbolID int64, opts TraversalOptions) []Path {
	if len(opts.Kinds) == 0 {
		opts = TraversalOptions{
			Kinds:         defaultBlastRadiusKinds,
			MinConfidence: opts.MinConfidence,
			MaxDepth:      opts.MaxDepth,
		}
	}
	return bfs(g, reverseAdjacency, symbolID, opts.MaxDepth, opts.MinConfidence, opts.allowsKind)
}
