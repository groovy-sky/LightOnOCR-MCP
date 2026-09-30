# syntax=docker/dockerfile:1.7

FROM golang:1.25.13-bookworm AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/lightonocr-mcp .

FROM debian:bookworm-slim AS llama-builder
ARG LLAMA_CPP_REVISION=6a2743f028f78bfb88a7189607b49bde30df3769
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates cmake g++ git make && rm -rf /var/lib/apt/lists/*
RUN test "$(printf '%s' "$LLAMA_CPP_REVISION" | wc -c)" -eq 40 && \
    git clone --filter=blob:none https://github.com/ggml-org/llama.cpp.git /src/llama.cpp && \
    git -C /src/llama.cpp checkout --detach "$LLAMA_CPP_REVISION"
RUN cmake -S /src/llama.cpp -B /src/llama.cpp/build \
      -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF -DGGML_CPU_ALL_VARIANTS=OFF \
      -DGGML_STATIC=ON -DBUILD_SHARED_LIBS=OFF -DLLAMA_BUILD_SERVER=ON \
      -DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF && \
    cmake --build /src/llama.cpp/build --config Release --target llama-server -j"$(nproc)" && \
    cp /src/llama.cpp/build/bin/llama-server /out-llama-server

FROM debian:bookworm-slim
ARG MODEL_REVISION=c97bd377f04481830395218fa8951df9deaba756
ARG LLAMA_CPP_REVISION=6a2743f028f78bfb88a7189607b49bde30df3769
ARG VERSION=dev
LABEL org.opencontainers.image.title="LightOnOCR MCP" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.source="https://github.com/lightonai/lightonocr-mcp" \
      org.opencontainers.image.licenses="Apache-2.0" \
      ai.lightonocr.model.revision="$MODEL_REVISION" \
      ai.lightonocr.llama-cpp.revision="$LLAMA_CPP_REVISION"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tini && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 lightonocr && useradd --system --uid 10001 --gid lightonocr --home /nonexistent lightonocr && \
    mkdir -p /models /licenses && cp /usr/share/common-licenses/Apache-2.0 /licenses/model-Apache-2.0 && \
    chown lightonocr:lightonocr /models
COPY --from=go-builder /out/lightonocr-mcp /usr/local/bin/lightonocr-mcp
COPY --from=llama-builder /out-llama-server /usr/local/bin/llama-server
COPY --from=llama-builder /src/llama.cpp/LICENSE /licenses/llama.cpp-LICENSE
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh
COPY --chown=lightonocr:lightonocr artifacts/LightOnOCR-2-1B-Q8_0.gguf /models/LightOnOCR-2-1B-Q8_0.gguf
COPY --chown=lightonocr:lightonocr artifacts/LightOnOCR-2-1B-mmproj-F16.gguf /models/LightOnOCR-2-1B-mmproj-F16.gguf
COPY --chown=lightonocr:lightonocr artifacts/manifest.json artifacts/manifest.sha256 /models/
ENV MCP_ADDR=:8080 MCP_PATH=/mcp LLAMA_URL=http://127.0.0.1:8000 \
    MODEL_PATH=/models/LightOnOCR-2-1B-Q8_0.gguf \
    MMPROJ_PATH=/models/LightOnOCR-2-1B-mmproj-F16.gguf \
    MAX_IMAGE_BYTES=20971520 OCR_TIMEOUT=600s MAX_TOKENS=4096 \
    LLAMA_THREADS=0 STARTUP_TIMEOUT=900
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15m --retries=3 CMD curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/entrypoint.sh"]
