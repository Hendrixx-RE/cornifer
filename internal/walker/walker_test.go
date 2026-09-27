package walker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkBasicDiscovery(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "__init__.py"), "")
	writeFile(t, filepath.Join(root, "pkg", "mod.py"), "x = 1\n")
	writeFile(t, filepath.Join(root, "pkg", "sub", "__init__.py"), "")
	writeFile(t, filepath.Join(root, "pkg", "sub", "leaf.py"), "y = 2\n")
	writeFile(t, filepath.Join(root, "standalone.py"), "z = 3\n")
	writeFile(t, filepath.Join(root, "README.md"), "not python\n")

	files, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatalf("Walk() error = %v", err)
	}

	wantPaths := []string{
		"pkg/__init__.py",
		"pkg/mod.py",
		"pkg/sub/__init__.py",
		"pkg/sub/leaf.py",
		"standalone.py",
	}
	if len(files) != len(wantPaths) {
		t.Fatalf("Walk() returned %d files, want %d: %+v", len(files), len(wantPaths), files)
	}
	for i, f := range files {
		if f.Path != wantPaths[i] {
			t.Errorf("files[%d].Path = %q, want %q", i, f.Path, wantPaths[i])
		}
		if f.Language != "python" {
			t.Errorf("files[%d].Language = %q, want python", i, f.Language)
		}
		if len(f.ContentHash) != 64 {
			t.Errorf("files[%d].ContentHash = %q, want 64 hex chars", i, f.ContentHash)
		}
	}

	moduleByPath := map[string]string{}
	for _, f := range files {
		moduleByPath[f.Path] = f.ModuleName
	}
	wantModules := map[string]string{
		"pkg/__init__.py":     "pkg",
		"pkg/mod.py":          "pkg.mod",
		"pkg/sub/__init__.py": "pkg.sub",
		"pkg/sub/leaf.py":     "pkg.sub.leaf",
		"standalone.py":       "standalone",
	}
	for path, want := range wantModules {
		if got := moduleByPath[path]; got != want {
			t.Errorf("ModuleName(%s) = %q, want %q", path, got, want)
		}
	}
}

func TestWalkContentHash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.py"), "hello")

	files, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	// sha256("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if files[0].ContentHash != want {
		t.Errorf("ContentHash = %q, want %q", files[0].ContentHash, want)
	}
}

func TestWalkRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "ignored.py\nsub/\n")
	writeFile(t, filepath.Join(root, "keep.py"), "")
	writeFile(t, filepath.Join(root, "ignored.py"), "")
	writeFile(t, filepath.Join(root, "sub", "also_ignored.py"), "")
	writeFile(t, filepath.Join(root, "nested"), "")
	os.Remove(filepath.Join(root, "nested"))
	writeFile(t, filepath.Join(root, "nested", ".gitignore"), "local.py\n")
	writeFile(t, filepath.Join(root, "nested", "local.py"), "")
	writeFile(t, filepath.Join(root, "nested", "keep2.py"), "")

	files, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	want := []string{"keep.py", "nested/keep2.py"}
	if len(got) != len(want) {
		t.Fatalf("Walk() paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWalkSkipsVendoredDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.py"), "")
	writeFile(t, filepath.Join(root, "__pycache__", "cached.py"), "")
	writeFile(t, filepath.Join(root, ".venv", "lib.py"), "")
	writeFile(t, filepath.Join(root, "testdata", "fixture.py"), "")

	files, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "keep.py" {
		t.Fatalf("Walk() = %+v, want only keep.py", files)
	}
}

func TestModuleNameEmptyOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if got := moduleName(root, filepath.Join(other, "f.py")); got != "" {
		t.Errorf("moduleName() = %q, want empty for path outside root", got)
	}
}
