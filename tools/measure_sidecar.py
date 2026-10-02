#!/usr/bin/env python3
"""Measure one real code-embedding request against a Cornifer sidecar.

The requested source files are sampled at deterministic offsets. This is a
throughput probe only; it never writes vectors or represents a retrieval eval.
"""

from __future__ import annotations

import argparse
import json
import time
import urllib.request
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", default="http://127.0.0.1:18080/embed")
    parser.add_argument("--chars", type=int, default=5000)
    parser.add_argument("paths", nargs="+", type=Path)
    args = parser.parse_args()

    texts: list[str] = []
    for path in args.paths:
        data = path.read_text(encoding="utf-8")
        for offset in (0, max(0, len(data) // 2)):
            texts.append(data[offset : offset + args.chars])
    payload = json.dumps({"inputs": texts}).encode()
    request = urllib.request.Request(args.endpoint, payload, {"Content-Type": "application/json"})
    started = time.monotonic()
    with urllib.request.urlopen(request, timeout=900) as response:
        vectors = json.load(response)
    elapsed = time.monotonic() - started
    dimensions = sorted({len(vector) for vector in vectors})
    print(f"inputs={len(texts)} dimensions={dimensions} elapsed_seconds={elapsed:.3f} inputs_per_second={len(texts)/elapsed:.3f}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
