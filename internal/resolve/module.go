package resolve

import "strings"

// ModuleNameFromPath maps a repo-relative, forward-slash path to a dotted
// module name using pure path arithmetic: "fastapi/routing.py" ->
// "fastapi.routing", "fastapi/__init__.py" -> "fastapi". Unlike
// internal/walker it does not check for __init__.py files on disk, so it
// assumes every directory is a package. Resolve itself trusts
// model.File.ModuleName; this helper exists for callers and tests that only
// have paths.
func ModuleNameFromPath(path string) string {
	p := strings.TrimSuffix(path, ".py")
	parts := strings.Split(p, "/")
	if n := len(parts); n > 0 && parts[n-1] == "__init__" {
		parts = parts[:n-1]
	}
	return strings.Join(parts, ".")
}

// ModulePathCandidates is the inverse of ModuleNameFromPath: the repo-relative
// paths a dotted module name may live at, in lookup order (plain module file
// first, then package __init__.py).
func ModulePathCandidates(module string) []string {
	base := strings.ReplaceAll(module, ".", "/")
	return []string{base + ".py", base + "/__init__.py"}
}

func topLevelName(module string) string {
	if i := strings.IndexByte(module, '.'); i >= 0 {
		return module[:i]
	}
	return module
}

func parentModule(module string) string {
	if i := strings.LastIndexByte(module, '.'); i >= 0 {
		return module[:i]
	}
	return ""
}

// stdlibModules holds top-level standard-library module names, used only to
// label external imports as stdlib vs third-party.
var stdlibModules = setOf(
	"__future__", "abc", "argparse", "array", "ast", "asyncio", "base64", "binascii", "bisect",
	"builtins", "bz2", "calendar", "cgi", "cmath", "codecs", "collections", "colorsys", "concurrent",
	"configparser", "contextlib", "contextvars", "copy", "csv", "ctypes", "dataclasses", "datetime",
	"decimal", "difflib", "dis", "email", "enum", "errno", "faulthandler", "fnmatch", "fractions",
	"functools", "gc", "getpass", "gettext", "glob", "gzip", "hashlib", "heapq", "hmac", "html",
	"http", "importlib", "inspect", "io", "ipaddress", "itertools", "json", "keyword", "linecache",
	"locale", "logging", "lzma", "marshal", "math", "mimetypes", "multiprocessing", "numbers",
	"operator", "os", "pathlib", "pickle", "pkgutil", "platform", "pprint", "queue", "random", "re",
	"reprlib", "secrets", "select", "selectors", "shlex", "shutil", "signal", "site", "smtplib",
	"socket", "sqlite3", "ssl", "stat", "statistics", "string", "struct", "subprocess", "sys",
	"sysconfig", "tempfile", "textwrap", "threading", "time", "timeit", "tkinter", "token",
	"tokenize", "tomllib", "traceback", "types", "typing", "unicodedata", "unittest", "urllib",
	"uuid", "warnings", "weakref", "webbrowser", "xml", "zipfile", "zlib", "zoneinfo",
)

var builtinNames = setOf(
	"abs", "aiter", "all", "anext", "any", "ascii", "bin", "bool", "breakpoint", "bytearray", "bytes",
	"callable", "chr", "classmethod", "compile", "complex", "delattr", "dict", "dir", "divmod",
	"enumerate", "eval", "exec", "filter", "float", "format", "frozenset", "getattr", "globals",
	"hasattr", "hash", "help", "hex", "id", "input", "int", "isinstance", "issubclass", "iter", "len",
	"list", "locals", "map", "max", "memoryview", "min", "next", "object", "oct", "open", "ord", "pow",
	"print", "property", "range", "repr", "reversed", "round", "set", "setattr", "slice", "sorted",
	"staticmethod", "str", "sum", "super", "tuple", "type", "vars", "zip", "__import__",
	"Exception", "BaseException", "ValueError", "TypeError", "KeyError", "IndexError", "RuntimeError",
	"AttributeError", "NotImplementedError", "StopIteration", "AssertionError", "ImportError",
	"OSError", "IOError", "LookupError", "ArithmeticError", "Warning", "DeprecationWarning",
	"UserWarning", "RuntimeWarning", "StopAsyncIteration", "KeyboardInterrupt", "SystemExit",
	"NameError", "ZeroDivisionError", "PermissionError", "FileNotFoundError", "TimeoutError",
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
