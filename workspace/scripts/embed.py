#!/usr/bin/env python3
"""
Long-running embedder daemon for giantmem.

Protocol:
    stdin:  one JSON object per line, `{"text": "..."}`
    stdout: one JSON object per line, `{"vec": [...]}` or `{"error": "..."}`

Args:
    --model NAME    sentence-transformers model id (default BAAI/bge-base-en-v1.5)
    --dim N         expected output dim — sanity check

Requires `sentence-transformers` in the active Python env.
"""

import argparse
import json
import sys
import traceback


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="BAAI/bge-base-en-v1.5")
    ap.add_argument("--dim", type=int, default=768)
    args = ap.parse_args()

    try:
        from sentence_transformers import SentenceTransformer
    except ImportError as e:
        print(json.dumps({"error": f"sentence-transformers not installed: {e}"}), flush=True)
        sys.exit(1)

    try:
        model = SentenceTransformer(args.model)
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"error": f"failed to load model {args.model}: {e}"}), flush=True)
        sys.exit(1)

    print(json.dumps({"ready": True, "model": args.model, "dim": args.dim}), flush=True)

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError as e:
            print(json.dumps({"error": f"bad json: {e}"}), flush=True)
            continue
        text = req.get("text", "")
        if not isinstance(text, str):
            print(json.dumps({"error": "text must be a string"}), flush=True)
            continue
        try:
            vec = model.encode(text, normalize_embeddings=True).tolist()
        except Exception as e:  # noqa: BLE001
            traceback.print_exc(file=sys.stderr)
            print(json.dumps({"error": f"encode failed: {e}"}), flush=True)
            continue
        if args.dim and len(vec) != args.dim:
            print(json.dumps({"error": f"dim mismatch: got {len(vec)}, expected {args.dim}"}), flush=True)
            continue
        print(json.dumps({"vec": vec}), flush=True)


if __name__ == "__main__":
    main()
