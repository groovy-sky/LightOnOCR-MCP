#!/bin/sh
set -eu

artifact_dir=${1:?usage: generate-manifest.sh ARTIFACT_DIR}
: "${MODEL_REPOSITORY:?MODEL_REPOSITORY is required}"
: "${MODEL_REVISION:?MODEL_REVISION is required}"
: "${LLAMA_CPP_REVISION:?LLAMA_CPP_REVISION is required}"
: "${TEXT_GGUF_TYPE:?TEXT_GGUF_TYPE is required}"

python3 - "$artifact_dir" <<'PY'
import hashlib
import json
import os
import pathlib
import sys

root = pathlib.Path(sys.argv[1])


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


files = []
for path in sorted(root.glob("*.gguf")):
    files.append({
        "name": path.name,
        "sha256": sha256_file(path),
        "size": path.stat().st_size,
        "included_in_runtime_image": path.name in {
            "LightOnOCR-2-1B-Q8_0.gguf",
            "LightOnOCR-2-1B-mmproj-F16.gguf",
        },
    })
if not files:
    raise SystemExit("no GGUF artifacts found")
manifest = {
    "schema_version": 1,
    "model_repository": os.environ["MODEL_REPOSITORY"],
    "model_revision": os.environ["MODEL_REVISION"],
    "model_license": "Apache-2.0",
    "llama_cpp_revision": os.environ["LLAMA_CPP_REVISION"],
    "text_gguf_type": os.environ["TEXT_GGUF_TYPE"],
    "artifacts": files,
}
parity_results = {}
for name, environment in (("f16", "F16_PARITY_RESULT"), ("q8_0", "Q8_PARITY_RESULT")):
    if result_path := os.environ.get(environment):
        parity_results[name] = json.loads(pathlib.Path(result_path).read_text())
if parity_results:
    manifest["parity"] = parity_results
(root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
runtime_names = {"LightOnOCR-2-1B-Q8_0.gguf", "LightOnOCR-2-1B-mmproj-F16.gguf", "manifest.json"}
with (root / "manifest.sha256").open("w") as output:
    for path in sorted(root / name for name in runtime_names):
        if not path.is_file():
            raise SystemExit(f"missing runtime artifact: {path.name}")
        output.write(f"{sha256_file(path)}  {path.name}\n")
PY
