package indexer

import (
	"context"
	"fmt"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/store"
)

// remapAndInsertSymbols implements the ID-remapping contract internal/symbols
// doc.go assigns to "the store": internal/symbols.Extract hands back
// symbols with process-local temporary IDs (module symbol first, ID 1,
// strictly increasing in append order — see Extract's doc comment) and
// ParentID values that are temporary IDs within that same per-file slice.
// internal/store.Store.InsertSymbols reserves real IDs and writes
// ParentID as given, without remapping it (see internal/store/doc.go's
// note on chunk line-range persistence and this wave's Store gaps).
//
// perFile holds every file's freshly extracted symbols, each slice still in
// Extract's emission order (parent always precedes its children in that
// order, but only within one file — there is no ordering relationship
// across files). remapAndInsertSymbols inserts them level by level (module
// symbols first, since ParentID is nil only for those; then every symbol
// whose ParentID is already known to be real; and so on), rewriting each
// symbol's ParentID to its parent's real ID immediately before that symbol
// is inserted. This needs only as many InsertSymbols round trips as the
// deepest nesting level actually present (small: a handful of levels even
// for deeply nested Python), not one per symbol or one per file, and
// mutates every symbol in perFile in place to carry its final, repo-unique
// ID — exactly the shape internal/resolve.FileInput.Symbols requires.
func remapAndInsertSymbols(ctx context.Context, st store.Store, perFile [][]*model.Symbol) error {
	type key struct {
		file int
		temp int64
	}

	total := 0
	tempID := make([][]int64, len(perFile)) // tempID[file][idx] = original (pre-mutation) ID
	for fi, syms := range perFile {
		tempID[fi] = make([]int64, len(syms))
		for i, s := range syms {
			tempID[fi][i] = s.ID
		}
		total += len(syms)
	}

	realID := make(map[key]int64, total)
	done := make(map[key]bool, total)

	for len(done) < total {
		var level []*model.Symbol
		var levelKeys []key

		for fi, syms := range perFile {
			for i, s := range syms {
				k := key{fi, tempID[fi][i]}
				if done[k] {
					continue
				}
				if s.ParentID != nil {
					pk := key{fi, *s.ParentID}
					real, ok := realID[pk]
					if !ok {
						continue // parent not resolved yet; try again next level
					}
					remapped := real
					s.ParentID = &remapped
				}
				level = append(level, s)
				levelKeys = append(levelKeys, k)
			}
		}

		if len(level) == 0 {
			// Every remaining symbol is waiting on a parent that was never
			// inserted — a temp ID referencing a symbol outside its own
			// file's slice, which internal/symbols never produces. Fail
			// loudly rather than loop forever.
			return fmt.Errorf("indexer: cannot remap symbol IDs: %d symbol(s) reference an unresolved parent", total-len(done))
		}

		if err := st.InsertSymbols(ctx, level); err != nil {
			return fmt.Errorf("indexer: insert symbols: %w", err)
		}

		for i, s := range level {
			realID[levelKeys[i]] = s.ID
			done[levelKeys[i]] = true
		}
	}

	return nil
}
