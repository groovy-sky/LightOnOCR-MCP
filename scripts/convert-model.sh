#!/bin/sh
set -eu

: "${MODEL_REPOSITORY:=lightonai/LightOnOCR-2-1B}"
: "${MODEL_REVISION:=c97bd377f04481830395218fa8951df9deaba756}"
: "${LLAMA_CPP_REVISION:=6a2743f028f78bfb88a7189607b49bde30df3769}"
: "${TEXT_GGUF_TYPE:=Q8_0}"
: "${WORK_DIR:=/workspace}"
: "${ARTIFACT_DIR:=$WORK_DIR/artifacts}"

for revision in "$MODEL_REVISION" "$LLAMA_CPP_REVISION"; do
    case "$revision" in *[!0-9a-f]*) valid_revision=false ;; *) valid_revision=true ;; esac
    if [ "${#revision}" -ne 40 ] || [ "$valid_revision" != true ]; then
        echo "MODEL_REVISION and LLAMA_CPP_REVISION must be full lowercase 40-character commit SHAs" >&2
        exit 2
    fi
done
if [ "$TEXT_GGUF_TYPE" != "Q8_0" ]; then
    echo "only the parity-gated Q8_0 runtime type is supported" >&2
    exit 2
fi
command -v hf >/dev/null
command -v git >/dev/null
command -v cmake >/dev/null

model_dir="$WORK_DIR/model"
llama_dir="$WORK_DIR/llama.cpp"
mkdir -p "$model_dir" "$ARTIFACT_DIR"

hf download "$MODEL_REPOSITORY" --revision "$MODEL_REVISION" --local-dir "$model_dir"
find "$model_dir" -type f ! -path '*/.cache/*' -print0 | sort -z | xargs -0 sha256sum > "$ARTIFACT_DIR/source-manifest.sha256"
(cd / && sha256sum --check "$ARTIFACT_DIR/source-manifest.sha256")

git clone --filter=blob:none https://github.com/ggml-org/llama.cpp.git "$llama_dir"
git -C "$llama_dir" checkout --detach "$LLAMA_CPP_REVISION"

cmake -S "$llama_dir" -B "$llama_dir/build" -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=OFF \
    -DLLAMA_BUILD_SERVER=ON -DLLAMA_BUILD_TESTS=OFF
cmake --build "$llama_dir/build" --config Release --target llama-server llama-quantize -j"$(nproc)"

text_f16="$ARTIFACT_DIR/LightOnOCR-2-1B-F16.gguf"
mmproj_expected="$ARTIFACT_DIR/mmproj-LightOnOCR-2-1B-mmproj-F16.gguf"
mmproj_final="$ARTIFACT_DIR/LightOnOCR-2-1B-mmproj-F16.gguf"
PYTHONPATH="$llama_dir/gguf-py" python3 "$llama_dir/convert_hf_to_gguf.py" \
    "$model_dir" --outfile "$text_f16" --outtype f16
PYTHONPATH="$llama_dir/gguf-py" python3 "$llama_dir/convert_hf_to_gguf.py" "$model_dir" \
    --outfile "$ARTIFACT_DIR/LightOnOCR-2-1B-mmproj-F16.gguf" --outtype f16 --mmproj
if [ -f "$mmproj_expected" ]; then mv "$mmproj_expected" "$mmproj_final"; fi
test -s "$text_f16"
test -s "$mmproj_final"

"$llama_dir/build/bin/llama-quantize" "$text_f16" "$ARTIFACT_DIR/LightOnOCR-2-1B-Q8_0.gguf" "$TEXT_GGUF_TYPE"
test -s "$ARTIFACT_DIR/LightOnOCR-2-1B-Q8_0.gguf"

MODEL_REPOSITORY="$MODEL_REPOSITORY" MODEL_REVISION="$MODEL_REVISION" \
LLAMA_CPP_REVISION="$LLAMA_CPP_REVISION" TEXT_GGUF_TYPE="$TEXT_GGUF_TYPE" \
    "$(dirname "$0")/generate-manifest.sh" "$ARTIFACT_DIR"
