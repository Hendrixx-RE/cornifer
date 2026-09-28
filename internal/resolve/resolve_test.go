package resolve

import (
	"testing"

	"github.com/Hendrixx-RE/cornifer/internal/graph"
	"github.com/Hendrixx-RE/cornifer/internal/model"
)

const (
	imports = model.EdgeKindImports
	calls   = model.EdgeKindCalls
	inherit = model.EdgeKindInherits
	implmts = model.EdgeKindImplements
	exact   = model.ConfidenceExact
	high    = model.ConfidenceHigh
	medium  = model.ConfidenceMedium
	low     = model.ConfidenceLow
)

func TestModulePathMapping(t *testing.T) {
	cases := map[string]string{
		"fastapi/routing.py":  "fastapi.routing",
		"fastapi/__init__.py": "fastapi",
		"a/b/c/__init__.py":   "a.b.c",
		"top.py":              "top",
	}
	for path, mod := range cases {
		if got := ModuleNameFromPath(path); got != mod {
			t.Errorf("ModuleNameFromPath(%q) = %q, want %q", path, got, mod)
		}
	}
	got := ModulePathCandidates("a.b.c")
	if len(got) != 2 || got[0] != "a/b/c.py" || got[1] != "a/b/c/__init__.py" {
		t.Errorf("ModulePathCandidates = %v", got)
	}
}

var importRepo = map[string]string{
	"pkg/__init__.py":     "from .core import Engine\nfrom . import util\n",
	"pkg/core.py":         "class Engine:\n    def run(self):\n        pass\n",
	"pkg/util.py":         "def helper():\n    pass\n",
	"pkg/sub/__init__.py": "",
	"pkg/sub/deep.py": `from .. import util
from ..core import Engine as E
import os
import requests.adapters as ra
from . import missing
from .. import nothing_here
from ... import too_far

def f():
    util.helper()
    E.run(None)
`,
	"app.py": `import pkg.util
import pkg.core as c
from pkg import Engine, util as u
from pkg.sub.deep import *
from os.path import join
import json

def main():
    pkg.util.helper()
    c.Engine.run(None)
    Engine()
    u.helper()
    f()
    join("a")
    json.dumps({})
`,
}

func TestImportForms(t *testing.T) {
	fx := buildFiles(t, importRepo)

	// import x / import x.y as z / from x import y as z / from . import x /
	// multi-dot relative / __init__ re-export.
	fx.wantEdge(t, imports, "app", "pkg.util", exact)
	fx.wantEdge(t, imports, "app", "pkg.core", exact)
	fx.wantEdge(t, imports, "app", "pkg", exact)
	fx.wantEdge(t, imports, "app", "pkg.sub.deep", exact)
	fx.wantEdge(t, imports, "pkg", "pkg.core", exact)
	fx.wantEdge(t, imports, "pkg", "pkg.util", exact)
	fx.wantEdge(t, imports, "pkg.sub.deep", "pkg.util", exact)
	fx.wantEdge(t, imports, "pkg.sub.deep", "pkg.core", exact)
	fx.wantEdge(t, imports, "pkg.sub.deep", "pkg", exact)
	fx.noEdge(t, imports, "pkg", "pkg") // `from . import x` in __init__ is not a self-import

	// External imports are recorded as such, not resolved.
	fx.wantUnresolved(t, imports, "pkg.sub.deep", "os", true)
	fx.wantUnresolved(t, imports, "pkg.sub.deep", "requests.adapters", true)
	fx.wantUnresolved(t, imports, "app", "os.path.join", true)
	// In-repo misses are genuine unresolved refs, spelled as written.
	fx.wantUnresolved(t, imports, "pkg.sub.deep", ".missing", false)
	fx.wantUnresolved(t, imports, "pkg.sub.deep", "..nothing_here", false)
	fx.wantUnresolved(t, imports, "pkg.sub.deep", "...too_far", false)

	kinds := map[string]ExternalKind{}
	for _, r := range fx.out.Imports {
		kinds[r.Name] = r.External
	}
	if kinds["os"] != ExternalStdlib || kinds["requests.adapters"] != ExternalThirdParty || kinds["json"] != ExternalStdlib {
		t.Errorf("external classification = %v", kinds)
	}

	// Bindings feed call resolution.
	fx.wantEdge(t, calls, "app.main", "pkg.util.helper", exact)
	fx.wantEdge(t, calls, "app.main", "pkg.core.Engine", exact) // through pkg/__init__ re-export
	fx.wantEdge(t, calls, "app.main", "pkg.core.Engine.run", exact)
	fx.wantEdge(t, calls, "pkg.sub.deep.f", "pkg.util.helper", exact)
	fx.wantEdge(t, calls, "pkg.sub.deep.f", "pkg.core.Engine.run", exact)
	fx.wantUnresolved(t, calls, "app.main", "join", true)
	fx.wantUnresolved(t, calls, "app.main", "json.dumps", true)
	// `f()` only exists via `from pkg.sub.deep import *`, at reduced confidence.
	fx.wantEdge(t, calls, "app.main", "pkg.sub.deep.f", high)
}

func TestStarImportAndReexport(t *testing.T) {
	fx := buildFiles(t, map[string]string{
		"lib/__init__.py": "from .impl import *\n",
		"lib/impl.py":     "def api():\n    pass\n",
		"use.py":          "from lib import api\nimport lib\n\ndef go():\n    api()\n    lib.api()\n",
	})
	// Reached through a star import in __init__.py: High, not Exact, since
	// any star import could have supplied the name.
	fx.wantEdge(t, calls, "use.go", "lib.impl.api", high)
	fx.wantEdge(t, imports, "lib", "lib.impl", exact)
}

func TestCallResolutionPaths(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
def top():
    pass

class Base:
    def a(self):
        pass
    def b(self):
        self.a()
    def only_here(self):
        pass

class Child(Base):
    def a(self):
        super().a()
    def c(self, top2, obj):
        self.b()
        self.a()
        top()
        Child.a(self)
        Base.b(self)
        obj.a()
        obj.only_here()
        obj.zzz()
        len(top2)
        self.missing()
        top2()

def shadow(top):
    top()
`})
	fx.wantEdge(t, calls, "m.Base.b", "m.Base.a", exact)           // self.m() on own class
	fx.wantEdge(t, calls, "m.Child.c", "m.Base.b", exact)          // self.b() via hierarchy (High) merged with Base.b(self) (Exact): max wins
	fx.wantEdge(t, calls, "m.Child.c", "m.Child.a", exact)         // self.m() own override
	fx.wantEdge(t, calls, "m.Child.c", "m.top", exact)             // lexical scope
	fx.wantEdge(t, calls, "m.Child.a", "m.Base.a", exact)          // super(), single resolved base
	fx.wantEdge(t, calls, "m.Child.c", "m.Base.only_here", medium) // unknown receiver, unique name
	// Unknown receiver, two candidate classes: both linked, lowest tier.
	fx.wantEdge(t, calls, "m.Child.c", "m.Base.a", low)
	fx.wantEdge(t, calls, "m.Child.c", "m.Child.a", exact) // exact edge wins over Low dup

	fx.wantUnresolved(t, calls, "m.Child.c", "obj.zzz", false)
	fx.wantUnresolved(t, calls, "m.Child.c", "self.missing", false)
	fx.wantUnresolved(t, calls, "m.Child.c", "len", true)
	fx.wantUnresolved(t, calls, "m.Child.c", "top2", false) // parameter: dynamic
	// A parameter named like a module function must not bind to it.
	fx.noEdge(t, calls, "m.shadow", "m.top")
	fx.wantUnresolved(t, calls, "m.shadow", "top", false)
}

func TestSelfCallToSubclassOverride(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
class Base:
    def run(self):
        self.hook()

class Impl(Base):
    def hook(self):
        pass
`})
	fx.wantEdge(t, calls, "m.Base.run", "m.Impl.hook", medium)
}

func TestExternalBases(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
import pydantic
from typing import Generic, TypeVar

T = TypeVar("T")

class Model(pydantic.BaseModel, Generic[T]):
    def go(self):
        self.model_dump()
        super().__init__()

class Plain:
    def go(self):
        super().__init__()
        self.nope()
`})
	fx.wantUnresolved(t, inherit, "m.Model", "pydantic.BaseModel", true)
	fx.wantUnresolved(t, inherit, "m.Model", "Generic", true)
	fx.wantUnresolved(t, calls, "m.Model.go", "self.model_dump", true) // inherited from external base
	fx.wantUnresolved(t, calls, "m.Model.go", "super().__init__", true)
	fx.wantUnresolved(t, calls, "m.Plain.go", "super().__init__", true) // object.__init__
	fx.wantUnresolved(t, calls, "m.Plain.go", "self.nope", false)
}

func TestRedefinedNamePicksLastAtReducedConfidence(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
def dup():
    pass

def dup():
    pass

def caller():
    dup()
`})
	if got := len(fx.edges(calls)); got != 1 {
		t.Fatalf("got %d call edges, want 1:\n%s", got, fx.dump(calls))
	}
	fx.wantEdge(t, calls, "m.caller", "m.dup", high)
}

func TestInheritsAndDiamond(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
class A:
    def f(self):
        pass
    def only_a(self):
        pass

class B(A):
    def f(self):
        pass

class C(A):
    def f(self):
        pass

class D(B, C):
    def f(self):
        pass

class F(B, C):
    def g(self):
        self.f()
        self.only_a()
        super().f()
`})
	fx.wantEdge(t, inherit, "m.B", "m.A", exact)
	fx.wantEdge(t, inherit, "m.C", "m.A", exact)
	fx.wantEdge(t, inherit, "m.D", "m.B", exact)
	fx.wantEdge(t, inherit, "m.D", "m.C", exact)

	// Diamond: D.f overrides the definer along each branch, never A.f directly.
	fx.wantEdge(t, implmts, "m.B.f", "m.A.f", exact)
	fx.wantEdge(t, implmts, "m.C.f", "m.A.f", exact)
	fx.wantEdge(t, implmts, "m.D.f", "m.B.f", exact)
	fx.wantEdge(t, implmts, "m.D.f", "m.C.f", exact)
	fx.noEdge(t, implmts, "m.D.f", "m.A.f")

	// C3 order F, B, C, A: self.f() finds B.f first; only_a walks to A.
	fx.wantEdge(t, calls, "m.F.g", "m.B.f", high)
	fx.wantEdge(t, calls, "m.F.g", "m.A.only_a", high)
	fx.noEdge(t, calls, "m.F.g", "m.C.f")
}

func TestDeepOverrideChainAndCallersThroughInterface(t *testing.T) {
	fx := buildFiles(t, map[string]string{
		"shapes.py": `
class L0:
    def m(self):
        pass

class L1(L0):
    def m(self):
        pass

class L2(L1):
    pass

class L3(L2):
    def m(self):
        pass

class L4(L3):
    def m(self):
        pass
`,
		"client.py": `
from shapes import L4, L0

def use_leaf(x):
    L4.m(x)

def use_base(x):
    L0.m(x)

def unrelated():
    pass
`,
	})
	// A gap in the chain (L2 has no m) is skipped: L3.m overrides L1.m, and
	// the link is a hierarchy walk rather than a direct-base hit.
	fx.wantEdge(t, implmts, "shapes.L1.m", "shapes.L0.m", exact)
	fx.wantEdge(t, implmts, "shapes.L3.m", "shapes.L1.m", high)
	fx.wantEdge(t, implmts, "shapes.L4.m", "shapes.L3.m", exact)
	fx.noEdge(t, implmts, "shapes.L4.m", "shapes.L1.m")

	// The headline demo: callers of the interface method include callers that
	// only ever call an override, via graph.Callers following Implements.
	g := graph.Build(fx.out.Edges)
	paths := graph.Callers(g, fx.id["shapes.L0.m"], graph.TraversalOptions{MaxDepth: 10, Kinds: []model.EdgeKind{calls}})
	reached := map[string]bool{}
	for _, p := range paths {
		reached[fx.qname[p.End()]] = true
	}
	for _, want := range []string{"client.use_leaf", "client.use_base", "shapes.L4.m", "shapes.L3.m", "shapes.L1.m"} {
		if !reached[want] {
			t.Errorf("Callers(L0.m) did not reach %s; reached %v", want, reached)
		}
	}
	if reached["client.unrelated"] {
		t.Errorf("Callers(L0.m) reached client.unrelated")
	}
	// Callers of the deepest override must not include callers of the base.
	paths = graph.Callers(g, fx.id["shapes.L4.m"], graph.TraversalOptions{MaxDepth: 10, Kinds: []model.EdgeKind{calls}})
	for _, p := range paths {
		if fx.qname[p.End()] == "client.use_base" {
			t.Errorf("Callers(L4.m) reached client.use_base")
		}
	}
}

func TestNestedSymbolsDoNotBreakResolution(t *testing.T) {
	// A def nested in a function body currently has no symbol; its calls are
	// attributed to the enclosing symbol and its own name is a local, not a
	// global lookup.
	fx := buildFiles(t, map[string]string{"m.py": `
def helper():
    pass

def outer():
    def helper():
        pass
    helper()
    return helper
`})
	fx.noEdge(t, calls, "m.outer", "m.helper")
	fx.wantUnresolved(t, calls, "m.outer", "helper", false)
}

func TestStatsAndNoSilentDrops(t *testing.T) {
	fx := buildFiles(t, importRepo)
	s := fx.out.Stats
	c := s.ByKind[calls]
	if c == nil || c.Resolved == 0 || c.External == 0 {
		t.Fatalf("call stats = %+v", c)
	}
	var edgeSum int
	for _, n := range s.EdgesByConfidence {
		edgeSum += n
	}
	if edgeSum != len(fx.out.Edges) {
		t.Errorf("confidence histogram sums to %d, want %d edges", edgeSum, len(fx.out.Edges))
	}
	// Every unresolved-classified reference has a row (rows are deduplicated,
	// so rows <= references but never zero when references exist).
	var refs int
	for _, k := range s.ByKind {
		refs += k.External + k.Unresolved
	}
	if refs > 0 && len(fx.out.UnresolvedRefs) == 0 {
		t.Errorf("%d unresolved/external references counted but no rows emitted", refs)
	}
	if s.String() == "" {
		t.Errorf("Stats.String() is empty")
	}
}

func TestResolveRejectsBadInput(t *testing.T) {
	if _, err := Resolve([]FileInput{{}}); err == nil {
		t.Errorf("Resolve with nil File/Result: want error")
	}
}

func TestConstructorLocals(t *testing.T) {
	fx := buildFiles(t, map[string]string{"m.py": `
from starlette.testclient import TestClient

class Engine:
    def start(self):
        pass

class Sub(Engine):
    pass

client = TestClient(None)

def one():
    e = Sub()
    e.start()
    client.get("/")

def twice():
    e = Sub()
    e = other()
    e.start()
`})
	// A local bound once to an in-repo class resolves through its MRO.
	fx.wantEdge(t, calls, "m.one", "m.Engine.start", high)
	// A module-level variable bound to an external class stays external,
	// instead of being guessed onto an unrelated in-repo method.
	fx.wantUnresolved(t, calls, "m.one", "client.get", true)
	// Rebinding makes the type unknown: fall back to the name-only guess.
	fx.wantEdge(t, calls, "m.twice", "m.Engine.start", medium)
}
