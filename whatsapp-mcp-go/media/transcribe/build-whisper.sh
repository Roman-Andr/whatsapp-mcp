#!/usr/bin/env bash
# Builds whisper.cpp's whisper-cli, which the transcribe package runs for every
# transcription. Needs git, a C/C++ compiler and cmake (when cmake is missing it
# is taken from PyPI with uvx). No GPU, BLAS or ffmpeg is used: the package hands
# whisper-cli a ready 16 kHz WAV.
#
# Usage: build-whisper.sh [prefix]   (default: $XDG_DATA_HOME/whatsapp-mcp/whisper.cpp)
# Env:   WHISPER_CPP_VERSION (default v1.9.5), JOBS (default 4)
# Result: <prefix>/bin/whisper-cli; point WHATSAPP_WHISPER_CLI at it or put it on PATH.
set -euo pipefail

version=${WHISPER_CPP_VERSION:-v1.9.5}
prefix=${1:-${XDG_DATA_HOME:-$HOME/.local/share}/whatsapp-mcp/whisper.cpp}
jobs=${JOBS:-4}
src="$prefix/src"

cmake=(cmake)
if ! command -v cmake >/dev/null; then
  command -v uvx >/dev/null || { echo "cmake (or uvx to fetch it) is required" >&2; exit 1; }
  cmake=(uvx cmake)
fi

if [[ ! -d $src/.git ]]; then
  git clone --quiet --depth 1 --branch "$version" https://github.com/ggml-org/whisper.cpp "$src"
else
  git -C "$src" fetch --quiet --depth 1 origin tag "$version"
  git -C "$src" checkout --quiet "$version"
fi

# Static libraries so the binary can be moved; GGML_NATIVE tunes for this CPU,
# which is what a local tool wants (set GGML_NATIVE=OFF for a portable build).
"${cmake[@]}" -S "$src" -B "$src/build" -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF \
  -DWHISPER_SDL2=OFF -DWHISPER_FFMPEG=OFF -DGGML_NATIVE="${GGML_NATIVE:-ON}" >/dev/null
"${cmake[@]}" --build "$src/build" --target whisper-cli -j "$jobs"
mkdir -p "$prefix/bin"
install -m 0755 "$src/build/bin/whisper-cli" "$prefix/bin/whisper-cli"
echo "$prefix/bin/whisper-cli"
