#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${IMAGE:-tdx-api-stock-web:invest-grade-fixed}"
OUTPUT_DIR="${OUTPUT_DIR:-${ROOT_DIR}/dist/images}"

if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
  echo "missing local image: ${IMAGE}" >&2
  echo "build it first with ./scripts/build_local_image.sh" >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIR}"

safe_name="$(echo "${IMAGE}" | tr '/:' '__')"
output_file="${OUTPUT_FILE:-${OUTPUT_DIR}/${safe_name}.tar}"

docker save -o "${output_file}" "${IMAGE}"

echo "saved ${IMAGE} to ${output_file}"
echo "load on another machine with: docker load -i ${output_file}"
