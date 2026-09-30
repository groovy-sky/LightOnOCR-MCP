# LightOnOCR MCP

CPU-only HTTP [Model Context Protocol](https://modelcontextprotocol.io/) server
for `lightonai/LightOnOCR-2-1B`. The production image contains a Go MCP server,
a static `llama-server`, and release-validated GGUF artifacts. It contains no
Python, Transformers, PyTorch, CUDA, compiler, or source Safetensors.

## Interface

- Streamable HTTP MCP: `POST /mcp`
- Health: `GET /healthz`
- Tool: `ocr_document`
- Input: exactly one of `image_url` or `image_base64`
- Output: extracted text or Markdown

URL inputs must use HTTP(S), contain no credentials, and resolve only to public
addresses. Redirects are checked under the same policy. MCP bodies, image
downloads, llama.cpp responses, inference duration, and output tokens are
bounded. The llama.cpp process listens only on container loopback.

## Pinned Sources

| Component | Immutable revision |
| --- | --- |
| `lightonai/LightOnOCR-2-1B` | `c97bd377f04481830395218fa8951df9deaba756` |
| `ggml-org/llama.cpp` | `6a2743f028f78bfb88a7189607b49bde30df3769` |

Release artifacts are generated only from these official repositories. The
release workflow records revisions, file sizes, and SHA-256 hashes in
`/models/manifest.json`; startup verifies the runtime files against
`/models/manifest.sha256` before loading either model. The image includes the
model's declared Apache-2.0 license and the pinned llama.cpp license under
`/licenses`.

## Development

Go 1.25 or newer is required. Start a compatible local llama.cpp server with a
text GGUF and its multimodal projector on loopback, then run:

```bash
go test -race ./...
go vet ./...
go build ./...
LLAMA_URL=http://127.0.0.1:8000 go run .
```

Local artifact conversion is for development verification only. It requires
Python 3.12, `hf`, CMake, a C++ compiler, enough disk for the source and F16/Q8
artifacts, and enough CPU/RAM for conversion and parity runs.

```bash
python -m pip install huggingface-hub==1.0.1 pillow==12.0.0 \
	torch==2.9.0 transformers==5.0.0
WORK_DIR="$PWD/.cache/conversion" ARTIFACT_DIR="$PWD/artifacts" \
	scripts/convert-model.sh
```

A plain production-image build does not download or convert the model. Before
building, `artifacts/` must contain the verified outputs from the conversion
pipeline or the `verified-model-artifacts` artifact from a trusted release
workflow run:

```bash
test -s artifacts/LightOnOCR-2-1B-Q8_0.gguf
test -s artifacts/LightOnOCR-2-1B-mmproj-F16.gguf
test -s artifacts/manifest.sha256
(cd artifacts && sha256sum --check --strict manifest.sha256)
docker build -t lightonocr-mcp:local .
```

The Docker build intentionally fails when any of these files is absent. Do not
create placeholder model files or a replacement checksum manifest.

Do not publish locally built images. Production images are built and published
only by the protected release workflow. No local `docker login`, `docker push`,
personal registry token, or manually uploaded model artifact is part of the
release process.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `MCP_ADDR` | `:8080` | Go HTTP listen address |
| `MCP_PATH` | `/mcp` | Streamable HTTP endpoint |
| `LLAMA_URL` | `http://127.0.0.1:8000` | Loopback llama.cpp URL |
| `MODEL_PATH` | `/models/LightOnOCR-2-1B-Q8_0.gguf` | Runtime text model |
| `MMPROJ_PATH` | `/models/LightOnOCR-2-1B-mmproj-F16.gguf` | Runtime vision projector |
| `MAX_IMAGE_BYTES` | `20971520` | Decoded/downloaded image limit |
| `OCR_TIMEOUT` | `600s` in image | Download and inference timeout |
| `MAX_TOKENS` | `4096` | Maximum generated tokens |
| `LLAMA_THREADS` | `0` | CPU threads; zero lets llama.cpp choose |
| `STARTUP_TIMEOUT` | `900` | llama.cpp readiness deadline in seconds |

## Release

Create GitHub Environments named `release`, `staging`, and `production`.
Require reviewers for `release` and `production`; allow the `release`
environment only from `main` and version tags. Pull-request CI has only
`contents: read` and cannot publish. The model conversion job also has only
`contents: read`; `packages: write` is scoped exclusively to the protected
`publish` job that authenticates to GHCR with its workflow `GITHUB_TOKEN`.

No model conversion, Docker build, registry login, or inference run is required
on the operator's machine. Every push to `main` automatically starts the
protected workflow and publishes the tested image as
`ghcr.io/<owner>/lightonocr-mcp:sha-<git-sha>` after any configured environment
approval.

A signed semantic version tag additionally publishes the version tag and, for
a stable release, `latest`:

```bash
git tag -s v1.0.0 -m 'LightOnOCR MCP v1.0.0'
git push origin v1.0.0
```

The workflow can also be rerun explicitly from `main` without performing any
local build or registry operation:

```bash
gh auth login --hostname github.com
gh workflow run release.yml --ref main
gh run list --workflow release.yml --limit 5
gh run watch <run-id> --exit-status
```

Do not manually dispatch a tag immediately after pushing it, because the tag
push already starts the release workflow.

The workflow downloads the immutable model revision, records and verifies the
source manifest, converts F16 text and projector artifacts, explicitly creates
Q8_0, and compares deterministic llama.cpp output with Transformers output on
scan, table, multi-column, formula, and multilingual fixtures. F16 normalized
character error must be at most 1%; the separately gated Q8_0 threshold is 3%.
Missing Markdown headings, tables, or formula blocks fail either gate. Python
conversion dependencies are pinned by the workflow and are not modified by the
conversion script.

After parity, the workflow builds and inspects the image, rejects High or
Critical fixed vulnerabilities, creates an SPDX SBOM, runs CPU health and MCP
tool-call smoke tests for both base64 and URL images, and verifies graceful
`SIGTERM`. Only then does it push to GHCR with GitHub's workflow-scoped token,
attach provenance, and pass the immutable digest to protected staging.
The conversion job reclaims unused SDKs on its ephemeral hosted runner and
requires at least 30 GiB free before downloading the model. It uses
`ubuntu-24.04` by default. If the standard runner is insufficient for the
conversion or float32 reference pass, configure a GitHub larger runner and set
the repository variable `MODEL_BUILD_RUNNER` to that runner's label. This still
keeps conversion, image construction, testing, and GHCR publication entirely
inside GitHub Actions.

Published references are:

- `ghcr.io/<owner>/lightonocr-mcp:sha-<git-sha>`
- `ghcr.io/<owner>/lightonocr-mcp:<semver>` for version releases
- `ghcr.io/<owner>/lightonocr-mcp:latest` for stable, non-prerelease tags only

Set GHCR package visibility to public when anonymous pulls are required.

## Run A Published Image

Use the workflow-produced immutable SHA tag or, preferably, its digest:

```bash
docker pull ghcr.io/<owner>/lightonocr-mcp@sha256:<digest>
docker volume create lightonocr-cache
docker run --rm --name lightonocr-mcp \
	--read-only --tmpfs /tmp:rw,noexec,nosuid,size=64m \
	--mount type=volume,source=lightonocr-cache,target=/cache \
	--publish 127.0.0.1:8080:8080 \
	ghcr.io/<owner>/lightonocr-mcp@sha256:<digest>
```

The model is embedded; the cache volume is optional and no model download
occurs at startup. Check readiness and initialize MCP:

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/mcp \
	-H 'Content-Type: application/json' \
	-H 'Accept: application/json, text/event-stream' \
	--data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}'
```

## Deployment And Rollback

Configure each protected deployment environment with `DEPLOY_WEBHOOK_URL` and
`DEPLOY_TOKEN` secrets plus a `DEPLOY_HEALTH_URL` variable. The webhook must
roll out the supplied `image` field without converting its digest to a tag.

```bash
gh workflow run deploy.yml \
	-f environment=production \
	-f image_digest=sha256:<verified-digest>
```

Production requires environment approval. The workflow rejects tags, verifies
the digest can be pulled from GHCR and has GitHub build provenance for this
repository, invokes the deployment webhook, and waits for `/healthz`. Roll back
by running the same command with a previously tested and attested digest;
immutable tags and digests are never overwritten.