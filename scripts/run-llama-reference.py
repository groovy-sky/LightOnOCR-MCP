#!/usr/bin/env python3
import argparse
import base64
import json
from pathlib import Path
from urllib.request import Request, urlopen

parser = argparse.ArgumentParser()
parser.add_argument("--url", default="http://127.0.0.1:8000")
parser.add_argument("--fixtures", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--max-tokens", type=int, default=4096)
args = parser.parse_args()

results = {}
for image_path in sorted(Path(args.fixtures).glob("*.png")):
    encoded = base64.b64encode(image_path.read_bytes()).decode()
    payload = {"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": f"data:image/png;base64,{encoded}"}}]}], "max_tokens": args.max_tokens, "stream": False}
    request = Request(args.url.rstrip("/") + "/v1/chat/completions", json.dumps(payload).encode(), {"Content-Type": "application/json"})
    with urlopen(request, timeout=660) as response:
        body = json.load(response)
    results[image_path.name] = body["choices"][0]["message"]["content"]
Path(args.output).write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
