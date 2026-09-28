package resolve

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/Hendrixx-RE/cornifer/internal/model"
)

type nameAlias struct{ name, alias string }

// importRec is one import statement's payload, as written.
type importRec struct {
	from  bool
	level int    // leading dots of a relative import
	mod   string // dotted module as written, without leading dots
	alias string // plain `import a.b as alias`
	names []nameAlias
	star  bool
}

type starRec struct {
	modName  string
	external bool
}

// binding is what a local name introduced by an import refers to.
type binding struct {
	from    bool
	modName string // plain import: the bound module; from-import: source module
	name    string // from-import: imported name
	ext     ExternalKind
}

type vkind int

const (
	vkUnknown vkind = iota
	vkLocal
	vkSyms
	vkModule
	vkExternal
)

// value is what a name resolves to before any attribute/call is applied.
type value struct {
	kind    vkind
	syms    []*model.Symbol
	conf    model.Confidence
	modName string
	ctor    []string // vkLocal: constructor chain of a single-assignment `x = Ctor()`
}

func symsValue(syms []*model.Symbol, conf model.Confidence) value {
	if len(syms) > 1 {
		conf = min(conf, model.ConfidenceHigh)
	}
	return value{kind: vkSyms, syms: syms, conf: conf}
}

func externalKind(top string) ExternalKind {
	if stdlibModules[top] {
		return ExternalStdlib
	}
	return ExternalThirdParty
}

func (r *resolver) collectImports(fi *fileInfo) {
	src := fi.in.Result.Source
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Type() {
		case "import_statement":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(i)
				switch c.Type() {
				case "dotted_name":
					fi.imports = append(fi.imports, importRec{mod: c.Content(src)})
				case "aliased_import":
					nm, al := c.ChildByFieldName("name"), c.ChildByFieldName("alias")
					if nm != nil && al != nil {
						fi.imports = append(fi.imports, importRec{mod: nm.Content(src), alias: al.Content(src)})
					}
				}
			}
			return
		case "import_from_statement":
			rec := importRec{from: true}
			modNode := n.ChildByFieldName("module_name")
			if modNode != nil {
				if modNode.Type() == "relative_import" {
					for i := 0; i < int(modNode.NamedChildCount()); i++ {
						c := modNode.NamedChild(i)
						switch c.Type() {
						case "import_prefix":
							rec.level = len(strings.TrimSpace(c.Content(src)))
						case "dotted_name":
							rec.mod = c.Content(src)
						}
					}
				} else {
					rec.mod = modNode.Content(src)
				}
			}
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(i)
				if modNode != nil && c.StartByte() == modNode.StartByte() && c.EndByte() == modNode.EndByte() {
					continue
				}
				switch c.Type() {
				case "dotted_name":
					rec.names = append(rec.names, nameAlias{name: c.Content(src)})
				case "aliased_import":
					nm, al := c.ChildByFieldName("name"), c.ChildByFieldName("alias")
					if nm != nil {
						na := nameAlias{name: nm.Content(src)}
						if al != nil {
							na.alias = al.Content(src)
						}
						rec.names = append(rec.names, na)
					}
				case "wildcard_import":
					rec.star = true
				}
			}
			fi.imports = append(fi.imports, rec)
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(fi.in.Result.Tree.RootNode())
}

// absModule resolves rec's (possibly relative) module to an absolute dotted
// name. ok is false when a relative import cannot be anchored.
func (fi *fileInfo) absModule(rec importRec) (string, bool) {
	if rec.level == 0 {
		return rec.mod, true
	}
	if fi.modName == "" {
		return "", false
	}
	pkg := fi.modName
	if !fi.isInit {
		pkg = parentModule(pkg)
	}
	for i := 1; i < rec.level; i++ {
		if pkg == "" {
			return "", false
		}
		pkg = parentModule(pkg)
	}
	switch {
	case rec.mod == "":
		if pkg == "" {
			return "", false
		}
		return pkg, true
	case pkg == "":
		return rec.mod, true
	default:
		return pkg + "." + rec.mod, true
	}
}

func recName(rec importRec, name string) string {
	prefix := strings.Repeat(".", rec.level) + rec.mod
	switch {
	case name == "":
		return prefix
	case rec.mod == "":
		return prefix + name
	default:
		return prefix + "." + name
	}
}

func (r *resolver) importRef(fi *fileInfo, name string, rec ImportRecord, external bool) {
	rec.SrcSymbolID = fi.module.ID
	rec.Name = name
	r.out.Imports = append(r.out.Imports, rec)
	ks := r.out.Stats.kind(model.EdgeKindImports)
	switch {
	case rec.Resolved:
		ks.Resolved++
	case external:
		ks.External++
		r.addUnresolved(fi.module.ID, name, model.EdgeKindImports, true)
	default:
		ks.Unresolved++
		r.addUnresolved(fi.module.ID, name, model.EdgeKindImports, false)
	}
}

func (r *resolver) importEdge(fi *fileInfo, target *fileInfo) {
	if target != nil && target.module.ID != fi.module.ID {
		r.addEdge(fi.module.ID, target.module.ID, model.EdgeKindImports, model.ConfidenceExact)
	}
}

func (r *resolver) bindImports(fi *fileInfo) {
	for _, rec := range fi.imports {
		if !rec.from {
			r.bindPlain(fi, rec)
		} else {
			r.bindFrom(fi, rec)
		}
	}
}

func (r *resolver) bindPlain(fi *fileInfo, rec importRec) {
	top := topLevelName(rec.mod)
	bound, modName := top, top
	if rec.alias != "" {
		bound, modName = rec.alias, rec.mod
	}
	if !r.topLevel[top] {
		ek := externalKind(top)
		fi.bindings[bound] = &binding{modName: modName, ext: ek}
		r.importRef(fi, rec.mod, ImportRecord{External: ek}, true)
		return
	}
	fi.bindings[bound] = &binding{modName: modName}
	if target := r.byModule[rec.mod]; target != nil {
		r.importEdge(fi, target)
		r.importRef(fi, rec.mod, ImportRecord{Resolved: true, TargetSymbolID: target.module.ID}, false)
		return
	}
	r.importRef(fi, rec.mod, ImportRecord{}, false)
}

func (r *resolver) bindFrom(fi *fileInfo, rec importRec) {
	abs, ok := fi.absModule(rec)
	if !ok {
		for _, na := range rec.names {
			r.importRef(fi, recName(rec, na.name), ImportRecord{}, false)
		}
		if rec.star {
			r.importRef(fi, recName(rec, "*"), ImportRecord{}, false)
		}
		return
	}
	top := topLevelName(abs)
	if rec.level == 0 && !r.topLevel[top] {
		ek := externalKind(top)
		for _, na := range rec.names {
			fi.bindings[bindName(na)] = &binding{from: true, modName: abs, name: na.name, ext: ek}
			r.importRef(fi, recName(rec, na.name), ImportRecord{External: ek}, true)
		}
		if rec.star {
			fi.stars = append(fi.stars, starRec{modName: abs, external: true})
			r.importRef(fi, recName(rec, "*"), ImportRecord{External: ek}, true)
		}
		return
	}
	target := r.byModule[abs]
	r.importEdge(fi, target)
	for _, na := range rec.names {
		fi.bindings[bindName(na)] = &binding{from: true, modName: abs, name: na.name}
	}
	if rec.star {
		fi.stars = append(fi.stars, starRec{modName: abs})
		if target != nil {
			r.importRef(fi, recName(rec, "*"), ImportRecord{Resolved: true, TargetSymbolID: target.module.ID}, false)
		} else {
			r.importRef(fi, recName(rec, "*"), ImportRecord{}, false)
		}
	}
}

func bindName(na nameAlias) string {
	if na.alias != "" {
		return na.alias
	}
	return na.name
}

// checkFromImports runs once every file's bindings exist, so re-exports
// through other files' __init__.py can be followed.
func (r *resolver) checkFromImports(fi *fileInfo) {
	for _, rec := range fi.imports {
		if !rec.from {
			continue
		}
		abs, ok := fi.absModule(rec)
		if !ok || (rec.level == 0 && !r.topLevel[topLevelName(abs)]) {
			continue
		}
		for _, na := range rec.names {
			v := r.resolveExport(abs, na.name, map[string]bool{})
			name := recName(rec, na.name)
			switch v.kind {
			case vkModule:
				t := r.byModule[v.modName]
				r.importEdge(fi, t)
				ir := ImportRecord{Resolved: t != nil}
				if t != nil {
					ir.TargetSymbolID = t.module.ID
				}
				r.importRef(fi, name, ir, false)
			case vkSyms:
				ir := ImportRecord{Resolved: true}
				if t := r.byModule[abs]; t != nil {
					ir.TargetSymbolID = t.module.ID
				}
				r.importRef(fi, name, ir, false)
			case vkExternal:
				r.importRef(fi, name, ImportRecord{External: ExternalThirdParty}, true)
			default:
				r.importRef(fi, name, ImportRecord{}, false)
			}
		}
	}
}

func (r *resolver) resolveBinding(b *binding, visited map[string]bool) value {
	if b.ext != ExternalNone {
		return value{kind: vkExternal}
	}
	if !b.from {
		return value{kind: vkModule, modName: b.modName}
	}
	return r.resolveExport(b.modName, b.name, visited)
}

// resolveExport looks up name as an attribute of module modName: a symbol
// defined there, a name it imports (an __init__.py re-export), a submodule,
// or a name pulled in by one of its star imports.
func (r *resolver) resolveExport(modName, name string, visited map[string]bool) value {
	key := modName + "\x00" + name
	if visited[key] {
		return value{}
	}
	visited[key] = true

	m := r.byModule[modName]
	if m != nil {
		if syms := r.children[m.module.ID][name]; len(syms) > 0 {
			return symsValue(syms, model.ConfidenceExact)
		}
		if b := m.bindings[name]; b != nil {
			if v := r.resolveBinding(b, visited); v.kind != vkUnknown {
				return v
			}
		}
	}
	if _, ok := r.byModule[modName+"."+name]; ok {
		return value{kind: vkModule, modName: modName + "." + name}
	}
	if m != nil {
		sawExternal := false
		for _, s := range m.stars {
			if s.external {
				sawExternal = true
				continue
			}
			if v := r.resolveExport(s.modName, name, visited); v.kind == vkSyms || v.kind == vkModule {
				v.conf = min(v.conf, model.ConfidenceHigh)
				return v
			}
		}
		if sawExternal {
			return value{kind: vkExternal}
		}
	}
	return value{}
}
