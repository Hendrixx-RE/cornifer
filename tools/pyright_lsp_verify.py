#!/usr/bin/env python3
"""Record independent Pyright LSP checks for Cornifer's structural labels."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path
from urllib.parse import quote


TARGETS = (
    ("fastapi/applications.py", 1056, "add_api_route"),
    ("fastapi/routing.py", 879, "add_api_route"),
    ("fastapi/routing.py", 1120, "include_router"),
    ("fastapi/routing.py", 217, "get_request_handler"),
    ("fastapi/routing.py", 143, "serialize_response"),
    ("fastapi/dependencies/utils.py", 257, "get_dependant"),
    ("fastapi/dependencies/utils.py", 562, "solve_dependencies"),
)


def uri(path: Path) -> str:
    return "file://" + quote(str(path.resolve()))


class LSP:
    def __init__(self) -> None:
        self.process = subprocess.Popen(
            ["pyright-langserver", "--stdio"], text=False,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.request_id = 0

    def send(self, message: dict) -> None:
        body = json.dumps(message).encode()
        self.process.stdin.write(f"Content-Length: {len(body)}\r\n\r\n".encode() + body)
        self.process.stdin.flush()

    def read(self) -> dict:
        headers: dict[str, str] = {}
        while True:
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError(self.process.stderr.read().decode() or "language server closed stdout")
            if line in (b"\r\n", b"\n"):
                break
            key, value = line.decode().split(":", 1)
            headers[key.lower()] = value.strip()
        return json.loads(self.process.stdout.read(int(headers["content-length"])))

    def request(self, method: str, params: dict) -> dict:
        self.request_id += 1
        request_id = self.request_id
        self.send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        while True:
            message = self.read()
            if message.get("id") == request_id:
                if "error" in message:
                    raise RuntimeError(f"{method}: {message['error']}")
                return message.get("result")

    def notify(self, method: str, params: dict) -> None:
        self.send({"jsonrpc": "2.0", "method": method, "params": params})

    def close(self) -> None:
        self.process.terminate()
        try:
            self.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.process.kill()


def contains_location(locations: list[dict] | None, file_uri: str, line: int) -> bool:
    for location in locations or []:
        rng = location.get("range", {})
        start, end = rng.get("start", {}), rng.get("end", {})
        if location.get("uri") == file_uri and start.get("line", -1) <= line <= end.get("line", -1):
            return True
    return False


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = args.repo.resolve()
    version = subprocess.check_output(["pyright", "--version"], text=True).strip()
    commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
    lsp = LSP()
    try:
        lsp.request("initialize", {
            "processId": None, "rootUri": uri(root), "workspaceFolders": [{"uri": uri(root), "name": root.name}],
            "capabilities": {"textDocument": {"definition": {"linkSupport": False}}},
            "clientInfo": {"name": "cornifer-pyright-verify", "version": "1"},
        })
        lsp.notify("initialized", {})
        checks = []
        for rel, one_based_line, symbol in TARGETS:
            path = root / rel
            lines = path.read_text(encoding="utf-8").splitlines()
            line = one_based_line - 1
            character = lines[line].index(symbol)
            document = uri(path)
            lsp.notify("textDocument/didOpen", {"textDocument": {"uri": document, "languageId": "python", "version": 1, "text": path.read_text(encoding="utf-8")}})
            position = {"line": line, "character": character}
            definition = lsp.request("textDocument/definition", {"textDocument": {"uri": document}, "position": position})
            references = lsp.request("textDocument/references", {"textDocument": {"uri": document}, "position": position, "context": {"includeDeclaration": True}})
            checks.append({
                "path": rel, "line": one_based_line, "symbol": symbol,
                "definition_contains_label": contains_location(definition, document, line),
                "references_contain_label": contains_location(references, document, line),
                "definition_count": len(definition or []), "reference_count": len(references or []),
            })
        report = {"tool": version, "commit": commit, "repo": str(root), "generated_at_unix": time.time(), "checks": checks}
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        failed = [item for item in checks if not item["definition_contains_label"] or not item["references_contain_label"]]
        print(f"checks={len(checks)} passed={len(checks)-len(failed)} output={args.output}")
        return 1 if failed else 0
    finally:
        lsp.close()


if __name__ == "__main__":
    sys.exit(main())
