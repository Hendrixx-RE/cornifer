package resolve

import (
	"errors"
	"fmt"
	"path"

	"github.com/Hendrixx-RE/cornifer/internal/model"
	"github.com/Hendrixx-RE/cornifer/internal/parse"
)

// FileInput is everything Resolve needs to know about one file.
type FileInput struct {
	// File is the file's metadata; File.ModuleName drives import resolution.
	File *model.File
	// Symbols are the file's symbols with final, repo-unique IDs and
	// ParentIDs already remapped (i.e. after the store assigned IDs; the
	// temporary per-file IDs internal/symbols.Extract emits must be remapped
	// to globally unique values first). Exactly one must be SymbolKindModule.
	Symbols []*model.Symbol
	// Result is the file's parse result. Resolve only reads it and does not
	// close Result.Tree.
	Result *parse.Result
}

// ExternalKind classifies an import that leaves the repo.
type ExternalKind string

const (
	// ExternalNone marks an import that resolved inside the repo.
	ExternalNone ExternalKind = ""
	// ExternalStdlib marks a standard-library import.
	ExternalStdlib ExternalKind = "stdlib"
	// ExternalThirdParty marks any other import whose top-level package is
	// not part of the indexed repo.
	ExternalThirdParty ExternalKind = "third-party"
)

// ImportRecord describes one imported name (or one module for a plain
// `import`) and how it was classified. It is richer than the model.Edge /
// model.UnresolvedRef rows, which cannot carry the external-vs-internal
// distinction.
type ImportRecord struct {
	// SrcSymbolID is the importing file's module symbol.
	SrcSymbolID int64
	// Name is the import as written, e.g. "os.path", ".utils.helper".
	Name string
	// Target is the resolved in-repo module symbol, or 0 when unresolved.
	TargetSymbolID int64
	// External is set when the import targets code outside the repo.
	External ExternalKind
	// Resolved reports whether Name was bound to something in the repo.
	Resolved bool
}

// Output is the result of Resolve.
type Output struct {
	// Edges are deduplicated by (src, dst, kind); the highest confidence
	// seen for a triple is kept. IDs are zero (assigned by the store).
	Edges []*model.Edge
	// UnresolvedRefs holds every reference that could not be bound to an
	// in-repo symbol, including references to external code. Rows are
	// deduplicated by (src, name, kind). Use IsExternal to tell references
	// to third-party/stdlib/builtin code from genuine resolution misses.
	UnresolvedRefs []*model.UnresolvedRef
	// Imports lists every import with its classification.
	Imports []ImportRecord
	// Stats counts references, not deduplicated rows.
	Stats Stats

	external map[*model.UnresolvedRef]bool
}

// IsExternal reports whether ref (one of o.UnresolvedRefs) targets code
// outside the indexed repo (stdlib, builtins, third-party) rather than being
// an in-repo reference the heuristics failed to bind.
func (o *Output) IsExternal(ref *model.UnresolvedRef) bool { return o.external[ref] }

// Resolve turns the imports, class definitions and call sites of files into
// model.Edge and model.UnresolvedRef rows.
//
// This is heuristic name resolution, not type inference. Names are bound
// through lexical scope, import bindings (including __init__.py re-exports
// and star imports), self./cls./super() lookups on the enclosing class, and
// a best-effort C3 walk of resolved base classes. Attribute calls on
// receivers of unknown type (`obj.method()`) fall back to matching the
// method name across the repo and are recorded with ConfidenceMedium/Low.
// Flow is ignored: imports anywhere in a file bind for the whole file, and
// conditional redefinitions resolve to the last definition. Dynamic
// dispatch, decorators that rewrite callables, and attribute types are not
// modeled, so recall is inherently incomplete; Output.Stats quantifies the
// gap.
//
// Symbol IDs must be unique across all of files.
func Resolve(files []FileInput) (*Output, error) {
	r := &resolver{
		byModule:      map[string]*fileInfo{},
		topLevel:      map[string]bool{},
		children:      map[int64]map[string][]*model.Symbol{},
		byID:          map[int64]*model.Symbol{},
		methodsByName: map[string][]*model.Symbol{},
		classes:       map[int64]*classInfo{},
		subclasses:    map[int64][]*model.Symbol{},
		edgeIdx:       map[edgeKey]*model.Edge{},
		unresSeen:     map[unresKey]bool{},
		out:           &Output{external: map[*model.UnresolvedRef]bool{}},
	}
	r.out.Stats.init()

	for _, in := range files {
		if in.File == nil || in.Result == nil {
			return nil, errors.New("resolve: FileInput requires File and Result")
		}
		fi := &fileInfo{
			in:       in,
			modName:  in.File.ModuleName,
			isInit:   path.Base(in.File.Path) == "__init__.py",
			bindings: map[string]*binding{},
			byName:   map[string][]*model.Symbol{},
		}
		for _, s := range in.Symbols {
			if s.Kind == model.SymbolKindModule {
				fi.module = s
			}
			if _, dup := r.byID[s.ID]; dup {
				return nil, fmt.Errorf("resolve: duplicate symbol ID %d (%s)", s.ID, s.QualifiedName)
			}
			r.byID[s.ID] = s
			if s.ParentID != nil {
				m := r.children[*s.ParentID]
				if m == nil {
					m = map[string][]*model.Symbol{}
					r.children[*s.ParentID] = m
				}
				m[s.Name] = append(m[s.Name], s)
			}
			fi.byName[s.Name] = append(fi.byName[s.Name], s)
			if s.Kind == model.SymbolKindMethod {
				r.methodsByName[s.Name] = append(r.methodsByName[s.Name], s)
			}
		}
		if fi.module == nil {
			return nil, fmt.Errorf("resolve: file %s has no module symbol", in.File.Path)
		}
		if fi.modName != "" {
			r.byModule[fi.modName] = fi
			r.topLevel[topLevelName(fi.modName)] = true
		}
		r.files = append(r.files, fi)
	}

	for _, fi := range r.files {
		r.collectImports(fi)
	}
	for _, fi := range r.files {
		r.bindImports(fi)
	}
	for _, fi := range r.files {
		r.checkFromImports(fi)
	}
	for _, fi := range r.files {
		r.walkFile(fi, r.onClass, nil)
	}
	r.emitImplements()
	for _, fi := range r.files {
		r.walkFile(fi, nil, r.onCall)
	}

	r.out.Stats.finish(r.out.Edges)
	return r.out, nil
}

type edgeKey struct {
	src, dst int64
	kind     model.EdgeKind
}

type unresKey struct {
	src  int64
	name string
	kind model.EdgeKind
}

type fileInfo struct {
	in       FileInput
	module   *model.Symbol
	modName  string
	isInit   bool
	imports  []importRec
	bindings map[string]*binding
	stars    []starRec
	byName   map[string][]*model.Symbol
}

type classInfo struct {
	sym         *model.Symbol
	bases       []*model.Symbol
	baseConf    []model.Confidence
	extBase     bool // some base is external or otherwise unbound
	mro         []*model.Symbol
	mroComputed bool
}

type resolver struct {
	files         []*fileInfo
	byModule      map[string]*fileInfo
	topLevel      map[string]bool
	children      map[int64]map[string][]*model.Symbol
	byID          map[int64]*model.Symbol
	methodsByName map[string][]*model.Symbol
	classes       map[int64]*classInfo
	subclasses    map[int64][]*model.Symbol
	edgeIdx       map[edgeKey]*model.Edge
	unresSeen     map[unresKey]bool
	out           *Output
	ctorDepth     int
}

func (r *resolver) addEdge(src, dst int64, kind model.EdgeKind, conf model.Confidence) {
	k := edgeKey{src, dst, kind}
	if e, ok := r.edgeIdx[k]; ok {
		if conf > e.Confidence {
			e.Confidence = conf
		}
		return
	}
	e := &model.Edge{SrcSymbolID: src, DstSymbolID: dst, Kind: kind, Confidence: conf}
	r.edgeIdx[k] = e
	r.out.Edges = append(r.out.Edges, e)
}

func (r *resolver) addUnresolved(src int64, name string, kind model.EdgeKind, external bool) {
	k := unresKey{src, name, kind}
	if r.unresSeen[k] {
		return
	}
	r.unresSeen[k] = true
	ref := &model.UnresolvedRef{SrcSymbolID: src, Name: name, Kind: kind}
	r.out.UnresolvedRefs = append(r.out.UnresolvedRefs, ref)
	if external {
		r.out.external[ref] = true
	}
}
