#!/usr/bin/env python3
"""Exercise every Cornifer MCP tool through the real stdio transport.

Run from the repository root after indexing a repository and setting the
usual CORNIFER_MCP_* environment (repo ID, database URL, BM25 cache, and any
matching sidecar settings). It intentionally uses only the Python standard
library so it can serve as a reproducible local demo check.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys


COMMAND = os.environ.get("CORNIFER_MCP_COMMAND", "go run ./cmd/cornifer-mcp").split()


def request(process: subprocess.Popen[str], request_id: int, method: str, params: dict) -> dict:
    process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}) + "\n")
    process.stdin.flush()
    while True:
        line = process.stdout.readline()
        if not line:
            stderr = process.stderr.read()
            raise RuntimeError(f"MCP server closed stdout while waiting for {method}: {stderr}")
        response = json.loads(line)
        if response.get("id") == request_id:
            if "error" in response:
                raise RuntimeError(f"{method}: {response['error']}")
            return response["result"]


def call_tool(process: subprocess.Popen[str], request_id: int, name: str, arguments: dict) -> None:
    result = request(process, request_id, "tools/call", {"name": name, "arguments": arguments})
    if result.get("isError"):
        raise RuntimeError(f"{name}: {result}")
    print(f"PASS {name}")


def main() -> int:
    process = subprocess.Popen(
        COMMAND,
        text=True,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    try:
        request(process, 1, "initialize", {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "cornifer-stdio-smoke", "version": "1"},
        })
        process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}}) + "\n")
        process.stdin.flush()
        tools = request(process, 2, "tools/list", {}).get("tools", [])
        names = {tool["name"] for tool in tools}
        expected = {
            "search_code", "find_definition", "find_references", "get_dependencies",
            "get_call_graph", "get_blast_radius", "find_cycles",
        }
        if names != expected:
            raise RuntimeError(f"tools/list = {sorted(names)}, want {sorted(expected)}")
        print("PASS tools/list (all seven tools)")
        calls = [
            ("search_code", {"query": "jsonable encoder", "limit": 3}),
            ("find_definition", {"symbol": "fastapi.encoders.jsonable_encoder"}),
            ("find_references", {"symbol": "fastapi.encoders.jsonable_encoder"}),
            ("get_dependencies", {"symbol": "fastapi.applications.FastAPI.add_api_route"}),
            ("get_call_graph", {"symbol": "fastapi.applications.FastAPI.add_api_route"}),
            ("get_blast_radius", {"symbol": "fastapi.routing.APIRouter.include_router"}),
            ("find_cycles", {}),
        ]
        for request_id, (name, args) in enumerate(calls, start=3):
            call_tool(process, request_id, name, args)
        return 0
    finally:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()


if __name__ == "__main__":
    sys.exit(main())
