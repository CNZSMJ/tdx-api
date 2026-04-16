#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET_IMAGE="${TARGET_IMAGE:-tdx-api-stock-web:invest-grade-fixed}"
TARGET_OS="${TARGET_OS:-linux}"

detect_arch() {
  case "$(uname -m)" in
    arm64|aarch64) echo "arm64" ;;
    x86_64|amd64) echo "amd64" ;;
    *)
      echo "unsupported host arch: $(uname -m)" >&2
      echo "set TARGET_ARCH explicitly, e.g. TARGET_ARCH=amd64" >&2
      exit 1
      ;;
  esac
}

TARGET_ARCH="${TARGET_ARCH:-$(detect_arch)}"
PLATFORM="${PLATFORM:-${TARGET_OS}/${TARGET_ARCH}}"
PULL_BASE_IMAGES="${PULL_BASE_IMAGES:-0}"

BUILD_CMD=(docker buildx build --platform "${PLATFORM}" --load)
if ! docker buildx version >/dev/null 2>&1; then
  BUILD_CMD=(docker build)
fi

if [[ "${PULL_BASE_IMAGES}" == "1" ]]; then
  BUILD_CMD+=(--pull)
fi

BUILD_CMD+=(
  --build-arg "TARGETOS=${TARGET_OS}"
  --build-arg "TARGETARCH=${TARGET_ARCH}"
  --tag "${TARGET_IMAGE}"
  --file "${ROOT_DIR}/Dockerfile"
  "${ROOT_DIR}"
)

echo "building ${TARGET_IMAGE} for ${PLATFORM}"
"${BUILD_CMD[@]}"
echo "built ${TARGET_IMAGE}"
