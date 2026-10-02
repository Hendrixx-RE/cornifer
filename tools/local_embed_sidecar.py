#!/usr/bin/env python3
"""Loopback-only embeddings sidecar for a reproducible local Cornifer eval.

The Go client posts {"inputs": [text, ...]} to /embed and accepts the bare
matrix returned here.  This server deliberately uses the model's documented
``encode`` method: Jina's remote implementation applies the documented mean
pooling.  The model is symmetric (the same encode call for queries/documents)
and pgvector uses cosine distance, so this server adds neither instruction
prefixes nor a second normalization policy.

Install dependencies into a local directory and run, for example:

  PYTHONPATH=/tmp/cornifer-eval/pydeps \\
  HF_HOME=/tmp/cornifer-eval/hf-cache \\
  python3 tools/local_embed_sidecar.py

No endpoint is exposed beyond the configured loopback host.
"""

from __future__ import annotations

import json
import os
import threading
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import torch
from transformers import AutoModel


MODEL_ID = os.environ.get("CORNIFER_SIDECAR_MODEL_ID", "jinaai/jina-embeddings-v2-base-code")
MODEL_REVISION = os.environ.get("CORNIFER_SIDECAR_MODEL_REVISION", "516f4baf13dec4ddddda8631e019b5737c8bc250")
MODEL_DIMENSION = 768
# The card documents 8,192 supported positions but training at 512. Default to
# that trained length for a bounded CPU evaluation; callers may raise it (up
# to 8,192) deliberately, and /metadata records the exact value used.
MAX_LENGTH = int(os.environ.get("CORNIFER_SIDECAR_MAX_LENGTH", "512"))
MAX_REQUEST_BATCH = int(os.environ.get("CORNIFER_SIDECAR_BATCH_SIZE", "8"))
TORCH_THREADS = int(os.environ.get("CORNIFER_SIDECAR_TORCH_THREADS", "0"))
TORCH_INTEROP_THREADS = int(os.environ.get("CORNIFER_SIDECAR_TORCH_INTEROP_THREADS", "0"))
HOST = os.environ.get("CORNIFER_SIDECAR_HOST", "127.0.0.1")
PORT = int(os.environ.get("CORNIFER_SIDECAR_PORT", "18080"))
HF_HOME = os.environ.get("HF_HOME")

if MAX_LENGTH <= 0 or MAX_LENGTH > 8192:
    raise SystemExit("CORNIFER_SIDECAR_MAX_LENGTH must be in 1..8192")
if MAX_REQUEST_BATCH <= 0:
    raise SystemExit("CORNIFER_SIDECAR_BATCH_SIZE must be positive")
if TORCH_THREADS < 0 or TORCH_INTEROP_THREADS < 0:
    raise SystemExit("CORNIFER_SIDECAR_TORCH_THREADS and CORNIFER_SIDECAR_TORCH_INTEROP_THREADS must be non-negative")
if HOST not in {"127.0.0.1", "::1", "localhost"}:
    raise SystemExit("refusing a non-loopback CORNIFER_SIDECAR_HOST")

if TORCH_THREADS:
    torch.set_num_threads(TORCH_THREADS)
if TORCH_INTEROP_THREADS:
    torch.set_num_interop_threads(TORCH_INTEROP_THREADS)

print(f"loading {MODEL_ID}@{MODEL_REVISION} on CPU (max_length={MAX_LENGTH})", flush=True)
MODEL = AutoModel.from_pretrained(
    MODEL_ID,
    revision=MODEL_REVISION,
    trust_remote_code=True,
    cache_dir=HF_HOME,
)
MODEL.eval()
MODEL_LOCK = threading.Lock()


def encode(texts: list[str]) -> list[list[float]]:
    vectors: list[list[float]] = []
    # Cornifer's Go batching can contain 128 chunks. Keep CPU-memory usage
    # bounded without changing their order or model behavior.
    with MODEL_LOCK, torch.inference_mode():
        for start in range(0, len(texts), MAX_REQUEST_BATCH):
            encoded = MODEL.encode(texts[start : start + MAX_REQUEST_BATCH], max_length=MAX_LENGTH)
            if hasattr(encoded, "detach"):
                encoded = encoded.detach().cpu()
            rows = encoded.tolist()
            for row in rows:
                if len(row) != MODEL_DIMENSION:
                    raise RuntimeError(f"model returned dimension {len(row)}, expected {MODEL_DIMENSION}")
                vectors.append([float(value) for value in row])
    return vectors


class Handler(BaseHTTPRequestHandler):
    server_version = "cornifer-local-embed/1"

    def log_message(self, fmt: str, *args: Any) -> None:
        print(f"{self.address_string()} {fmt % args}", flush=True)

    def _json(self, status: HTTPStatus, value: Any) -> None:
        body = json.dumps(value, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        if self.path != "/metadata":
            self._json(HTTPStatus.NOT_FOUND, {"error": "use /metadata or POST /embed"})
            return
        self._json(
            HTTPStatus.OK,
            {
                "model": MODEL_ID,
                "revision": MODEL_REVISION,
                "dimension": MODEL_DIMENSION,
                "pooling": "model encode (documented mean pooling)",
                "similarity": "cosine",
                "query_document_contract": "symmetric encode; no instruction prefix",
                "max_length": MAX_LENGTH,
                "max_request_batch": MAX_REQUEST_BATCH,
                "torch_threads": torch.get_num_threads(),
                "torch_interop_threads": torch.get_num_interop_threads(),
            },
        )

    def do_POST(self) -> None:
        if self.path != "/embed":
            self._json(HTTPStatus.NOT_FOUND, {"error": "POST /embed"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > 16 * 1024 * 1024:
                raise ValueError("Content-Length must be 1..16777216")
            payload = json.loads(self.rfile.read(length))
            inputs = payload.get("inputs")
            if not isinstance(inputs, list) or not inputs or not all(isinstance(text, str) for text in inputs):
                raise ValueError('body must be {"inputs": [non-empty strings...]}')
            self._json(HTTPStatus.OK, encode(inputs))
        except (ValueError, json.JSONDecodeError) as exc:
            self._json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
        except Exception as exc:  # Keep Go's retryable 5xx behavior useful.
            self._json(HTTPStatus.INTERNAL_SERVER_ERROR, {"error": f"embedding failed: {exc}"})


if __name__ == "__main__":
    print(f"serving http://{HOST}:{PORT}/embed", flush=True)
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
