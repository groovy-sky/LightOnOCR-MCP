# PLAN.md

## Goal

Build one ready-to-use, CPU-only Docker image exposing the official
`lightonai/LightOnOCR-2-1B` model as an HTTP MCP server without Python in the
runtime image. All release builds and publication must run through GitHub
Actions, and production images must be published to GitHub Container Registry
(GHCR). Local builds are for development only and must never be promoted as
release artifacts.

The release pipeline starts from an immutable revision of the official Hugging
Face repository, converts that checkpoint to llama.cpp-compatible GGUF
artifacts, validates native inference against the official Transformers
implementation, and ships only the Go server, llama.cpp runtime, and generated
model files.

## MVP Interface

- MCP endpoint: `POST /mcp`
- Health endpoint: `GET /healthz`
- Tool: `ocr_document`
- Input:
  - `image_url`, or
  - `image_base64`
- Output: OCR text or Markdown

## Architecture

### GitHub Actions Build and Release Pipeline

1. Trigger CI for pull requests without package-write permissions or image
   publication.
2. Trigger the protected release workflow from a signed version tag or an
   explicitly approved `workflow_dispatch` event.
3. Download `lightonai/LightOnOCR-2-1B` at a pinned, immutable Hugging Face
   commit SHA. Never convert from a mutable branch such as `main`.
4. Verify the downloaded snapshot against a generated SHA-256 manifest.
5. Build conversion tools from a pinned llama.cpp commit.
6. Convert the Qwen3 text decoder and tokenizer metadata to a text GGUF.
7. Convert the Pixtral vision encoder and multimodal projector to an `mmproj`
   GGUF.
8. Preserve an unquantized BF16 or F16 text GGUF as the conversion reference.
9. Optionally produce a CPU-oriented quantized text GGUF, initially `Q8_0`.
   Keep the vision/projector artifact at F16 or F32 unless parity tests justify
   another format.
10. Run the same OCR fixtures through official Transformers inference and
   llama.cpp inference. Reject artifacts that exceed the documented parity
   threshold or lose required Markdown structure.
11. Record the source model revision, llama.cpp revision, conversion options,
   artifact hashes, and parity results in a machine-readable manifest.
12. Build and scan the final `linux/amd64` image, generate an SBOM, and run CPU
    health and MCP smoke tests before publication.
13. Authenticate to GHCR with the workflow-scoped `GITHUB_TOKEN`, publish the
    tested image, and attach build provenance. Do not use a long-lived personal
    access token.
14. Pass the published image digest to a protected deployment workflow. Every
   environment rollout must deploy by digest from GHCR and record the GitHub
   deployment status; manual production rollout commands are unsupported.

The release workflow must use a protected GitHub Environment requiring manual
approval. It must declare least-privilege permissions (`contents: read`,
`packages: write`, `id-token: write`, and `attestations: write` where required),
pin third-party actions by full commit SHA, prevent concurrent releases, and
never publish code from an untrusted pull request.

Python and Transformers are permitted in conversion and validation stages.
They must not be copied into the production image.

### Runtime Image

The final image contains:

1. The Go MCP HTTP server.
2. A pinned, CPU-only `llama-server` binary.
3. The generated text GGUF and `mmproj` GGUF.
4. The artifact manifest and licenses required for redistribution.
5. An entrypoint that starts llama.cpp on loopback, waits for model readiness,
   starts the Go server, and forwards shutdown signals to both processes.

```text
MCP client -> Go HTTP/MCP server -> loopback llama-server -> GGUF + mmproj
```

The Go server remains responsible for MCP handling, URL security, image and
response size limits, timeouts, cancellation, health aggregation, and clear
upstream errors. The llama.cpp endpoint must bind only to `127.0.0.1` and must
not be published by the container.

## Model Provenance

- Source repository: `lightonai/LightOnOCR-2-1B`
- Source revision: an immutable full commit SHA pinned in the Dockerfile and
  artifact manifest
- Source format: official Safetensors and processor/tokenizer configuration
- Text architecture: Qwen3
- Vision architecture: Pixtral
- Runtime format: one text GGUF plus one `mmproj` GGUF
- Runtime engine: llama.cpp pinned to a tested full commit SHA

Community GGUF repositories may be used for investigation but must not supply
release artifacts. Release GGUF files must be generated from the pinned
official snapshot.

## Files

```text
.
├── Dockerfile
├── .github/
│   └── workflows/
│       ├── ci.yml
│       ├── release.yml
│       └── deploy.yml
├── go.mod
├── go.sum
├── main.go
├── main_test.go
├── entrypoint.sh
├── scripts/
│   ├── convert-model.sh
│   ├── generate-manifest.sh
│   └── validate-parity.py
├── testdata/
│   └── parity/
├── artifacts/
│   └── manifest.json
└── README.md
```

Generated model files must not be committed to Git. The release workflow must
generate them in dedicated build stages or consume workflow artifacts produced
by the same trusted workflow run. The final image must embed the verified GGUF
files so deployment requires only pulling one immutable GHCR image.

## Implementation Steps

1. Pin a known-compatible official model commit and llama.cpp commit.
2. Add a reproducible download step that rejects mutable revisions and verifies
   the complete source snapshot.
3. Implement text GGUF conversion for the Qwen3 decoder and tokenizer.
4. Implement `mmproj` conversion for the Pixtral vision encoder/projector.
5. Add optional text-model quantization, starting with `Q8_0`; make the chosen
   runtime artifact explicit rather than selecting the smallest file
   automatically.
6. Create representative OCR parity fixtures covering scans, tables,
   multi-column text, formulas, and supported languages.
7. Generate Transformers reference outputs from the same pinned checkpoint and
   deterministic generation settings.
8. Compare llama.cpp outputs with the references using normalized character
   error rate and structural checks. Default release threshold: at most 1%
   normalized character error for the unquantized conversion, with no missing
   tables, headings, or formula blocks. Establish and document a separate
   threshold before releasing a quantized artifact.
9. Replace the Python inference client in the Go server with a bounded llama.cpp
   HTTP client that propagates request cancellation and enforces response and
   inference time limits.
10. Add focused Go tests for successful OCR, validation failures, oversized
    inputs and responses, llama.cpp failures, cancellation, and timeouts.
11. Build a multi-stage image. Conversion and parity stages may contain Python;
    the final stage must contain no Python, PyTorch, Transformers, CUDA,
    compiler, or conversion tooling.
12. Run the final image as a non-root user, expose only the MCP port, add a
    health check, and implement graceful shutdown for both processes.
13. Add `.github/workflows/ci.yml` for formatting, tests, static checks, and a
   non-publishing container build on pull requests and branch pushes.
14. Add `.github/workflows/release.yml` for model conversion, parity checks,
   image scanning, smoke tests, SBOM generation, provenance attestation, and
   GHCR publication. Pin every action by full commit SHA.
15. Publish immutable `sha-<git-sha>` and version tags to
   `ghcr.io/<owner>/lightonocr-mcp`. Apply `latest` only to an approved stable
   release, and never deploy by the mutable `latest` tag.
16. Add `.github/workflows/deploy.yml` as a reusable and manually dispatchable
   workflow accepting an environment and verified GHCR image digest. Use
   protected GitHub Environments for staging and production, with approval and
   environment-specific credentials isolated from build jobs.
17. Document workflow invocation, approval, GHCR package visibility, immutable
   image pull, local CPU verification, deployment, rollback, health-check, and
   MCP client commands.

## Configuration

### Build and Release

```text
MODEL_REPOSITORY=lightonai/LightOnOCR-2-1B
MODEL_REVISION=<immutable full Hugging Face commit SHA>
LLAMA_CPP_REVISION=<immutable full Git commit SHA>
TEXT_GGUF_TYPE=Q8_0
PARITY_FIXTURES=/workspace/testdata/parity
REGISTRY=ghcr.io
IMAGE_NAME=${GITHUB_REPOSITORY_OWNER}/lightonocr-mcp
```

The Dockerfile or release workflow must provide concrete full SHA values. A
build must fail if either revision is empty, abbreviated, or names a branch or
tag. Repository variables may hold non-secret pinned revisions. Secrets must
not be passed as Docker build arguments or written to logs, manifests, layers,
artifacts, or image labels.

### Runtime

```text
MCP_ADDR=:8080
MCP_PATH=/mcp
LLAMA_URL=http://127.0.0.1:8000
MODEL_PATH=/models/LightOnOCR-2-1B-Q8_0.gguf
MMPROJ_PATH=/models/LightOnOCR-2-1B-mmproj-F16.gguf
MAX_IMAGE_BYTES=20971520
OCR_TIMEOUT=600s
MAX_TOKENS=4096
LLAMA_THREADS=0
STARTUP_TIMEOUT=900
```

`LLAMA_THREADS=0` means llama.cpp selects an appropriate CPU thread count.
Runtime startup must verify model hashes against the embedded artifact manifest
before reporting readiness.

## Workflow Publication and Deployment

Production images must be built and published only by
`.github/workflows/release.yml`. A release operator selects a version tag or
starts an approved manual workflow; the workflow validates, builds, scans,
attests, and pushes the image to GHCR. Direct local publication is unsupported.

```bash
gh workflow run release.yml --ref <version-tag>
```

The release workflow passes the published digest to `.github/workflows/deploy.yml`.
That workflow performs staging or production rollout through a protected GitHub
Environment. It must accept an image digest, never a mutable tag, and production
must require approval. The concrete rollout step depends on the selected hosting
platform and must be implemented before production deployment.

This command is for local verification of a workflow-produced image, not
deployment:

```bash
docker pull ghcr.io/<owner>/lightonocr-mcp:sha-<git-sha>
docker run --rm \
  --name lightonocr-mcp \
  -p 8080:8080 \
   ghcr.io/<owner>/lightonocr-mcp:sha-<git-sha>
```

The workflow must publish these references only after every release gate passes:

- `ghcr.io/<owner>/lightonocr-mcp:sha-<git-sha>` for immutable deployment
- `ghcr.io/<owner>/lightonocr-mcp:<semver>` for approved version releases
- `ghcr.io/<owner>/lightonocr-mcp:latest` only for the latest approved stable
   release

Rollback means redeploying a previously published immutable SHA or version tag;
the deployment workflow must resolve the selected version to its verified
digest and must never overwrite an immutable tag.

## Validation

The release workflow must run the following checks before its GHCR push step:

1. Verify source and generated artifact SHA-256 manifests.
2. Run Transformers and llama.cpp parity tests on every fixture.
3. Run `gofmt` on changed Go files.
4. Run `go test ./...`, `go vet ./...`, and `go build ./...`.
5. Build the production Docker image.
6. Inspect the final image and fail if Python, PyTorch, Transformers, CUDA,
   compilers, conversion scripts, or source Safetensors are present.
7. Start the CPU-only container and exercise `/healthz`, MCP initialization,
   and `ocr_document` with base64 and URL inputs.
8. Send `SIGTERM` and verify that both Go and llama.cpp exit cleanly.
9. Generate and retain an SBOM, scan the image for known vulnerabilities, and
   enforce the repository's documented severity policy.
10. Verify image labels, embedded model manifest, non-root user, exposed ports,
    and absence of credentials.
11. Publish to GHCR only after all checks pass, then create a provenance
    attestation tied to the pushed image digest.
12. Deploy only the attested digest through the protected deployment workflow,
   run post-deployment health checks, and report success or failure to the
   GitHub Environment.

## Acceptance Criteria

- [ ] The official model and llama.cpp revisions are pinned by full commit SHA.
- [ ] Pull-request workflows cannot publish packages or access release secrets.
- [ ] Only the protected release workflow can publish production images.
- [ ] Only the protected deployment workflow can roll out an image, and it uses
   an attested immutable GHCR digest.
- [ ] Release artifacts are generated from the pinned official Safetensors.
- [ ] The Qwen3 decoder is represented by a validated text GGUF.
- [ ] The Pixtral vision encoder/projector is represented by a validated
      `mmproj` GGUF.
- [ ] Every runtime artifact has a recorded and startup-verified SHA-256 hash.
- [ ] Unquantized llama.cpp output passes the Transformers parity threshold.
- [ ] Any shipped quantization has its own documented parity result.
- [ ] The final image contains no Python, PyTorch, Transformers, or CUDA runtime.
- [ ] The final image embeds the validated GGUF artifacts and needs no model
   download during deployment.
- [ ] The service runs on CPU without GPU device access.
- [ ] `/healthz` reports whether llama.cpp has loaded both model artifacts.
- [ ] `/mcp` supports MCP Streamable HTTP.
- [ ] `ocr_document` accepts exactly one of `image_url` or `image_base64`.
- [ ] Inputs, downloads, inference responses, cancellation, and timeouts are
      bounded and tested.
- [ ] The llama.cpp service is loopback-only and not exposed from the container.
- [ ] The container stops both processes cleanly.
- [ ] The tested image is published to GHCR with immutable SHA and version tags,
   an SBOM, OCI labels, and build provenance.
- [ ] Published images use `GITHUB_TOKEN`; no long-lived registry credential is
   stored in the repository or image.
- [ ] README contains copy-paste workflow invocation, GHCR pull, run, health,
   rollback, and MCP client commands using pinned revisions and no real
   credentials.
