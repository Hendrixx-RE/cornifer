package model

import "testing"

func TestSymbolKindValid(t *testing.T) {
	valid := []SymbolKind{SymbolKindModule, SymbolKindClass, SymbolKindFunction, SymbolKindMethod, SymbolKindVariable}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("SymbolKind(%q).Valid() = false, want true", k)
		}
	}
	if SymbolKind("bogus").Valid() {
		t.Error(`SymbolKind("bogus").Valid() = true, want false`)
	}
}

func TestEdgeKindValid(t *testing.T) {
	valid := []EdgeKind{EdgeKindImports, EdgeKindCalls, EdgeKindInherits, EdgeKindImplements}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("EdgeKind(%q).Valid() = false, want true", k)
		}
	}
	if EdgeKind("bogus").Valid() {
		t.Error(`EdgeKind("bogus").Valid() = true, want false`)
	}
}
