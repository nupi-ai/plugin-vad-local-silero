#!/usr/bin/env bash
# Download ONNX Runtime shared libraries for ALL target platforms.
# Used by GoReleaser before.hooks to prepare platform-specific archives.
#
# Downloads ORT for each target platform inline (with SHA256 verification).
# Libraries are placed in lib/<os>-<arch>/ directories matching the
# resolveORTLibPath() convention in ort_lib.go.
#
# Usage: ./scripts/download-ort-all.sh [version]
set -euo pipefail

ORT_VERSION="${1:-1.23.0}"

# Platform definitions: ORT_PLATFORM  GOOS  GOARCH  EXT
PLATFORMS=(
  "osx-arm64     darwin  arm64  dylib"
  "linux-x64     linux   amd64  so"
  "linux-aarch64 linux   arm64  so"
  "win-x64       windows amd64  dll"
)

# Pinned SHA256 checksums — must match download-ort.sh.
get_expected_sha256() {
  case "${1}:${2}" in
    osx-arm64:1.23.0)     echo "8182db0ebb5caa21036a3c78178f17fabb98a7916bdab454467c8f4cf34bcfdf" ;;
    linux-x64:1.23.0)     echo "b6deea7f2e22c10c043019f294a0ea4d2a6c0ae52a009c34847640db75ec5580" ;;
    linux-aarch64:1.23.0) echo "0b9f47d140411d938e47915824d8daaa424df95a88b5f1fc843172a75168f7a0" ;;
    win-x64:1.23.0)       echo "72c23470310ec79a7d42d27fe9d257e6c98540c73fa5a1db1f67f538c6c16f2f" ;;
    *) echo "" ;;
  esac
}

# compute_sha256 <file> — portable SHA256.
compute_sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "ERROR: neither shasum nor sha256sum found" >&2
    exit 1
  fi
}

echo "Downloading ONNX Runtime v${ORT_VERSION} for all target platforms..."

for entry in "${PLATFORMS[@]}"; do
  read -r ORT_PLATFORM GOOS GOARCH EXT <<< "${entry}"

  DEST_DIR="lib/${GOOS}-${GOARCH}"
  if [ "${GOOS}" = "windows" ]; then
    DEST_NAME="onnxruntime.dll"
    ARCHIVE_EXT="zip"
  else
    DEST_NAME="libonnxruntime.${EXT}"
    ARCHIVE_EXT="tgz"
  fi

  # Skip if already downloaded.
  if [ -f "${DEST_DIR}/${DEST_NAME}" ]; then
    echo "  [skip] ${DEST_DIR}/${DEST_NAME} already exists"
    continue
  fi

  URL="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-${ORT_PLATFORM}-${ORT_VERSION}.${ARCHIVE_EXT}"
  echo "  [${GOOS}-${GOARCH}] Downloading ${ORT_PLATFORM}..."

  TMP_DIR="$(mktemp -d)"
  trap "rm -rf ${TMP_DIR}" EXIT INT TERM

  ARCHIVE_FILE="${TMP_DIR}/ort.${ARCHIVE_EXT}"
  curl -fsSL "${URL}" -o "${ARCHIVE_FILE}"

  # Verify checksum.
  EXPECTED_SHA256="$(get_expected_sha256 "${ORT_PLATFORM}" "${ORT_VERSION}")"
  if [ -z "${EXPECTED_SHA256}" ]; then
    echo "  ERROR: No pinned SHA256 for ${ORT_PLATFORM}:${ORT_VERSION}"
    exit 1
  fi
  ACTUAL_SHA256="$(compute_sha256 "${ARCHIVE_FILE}")"
  if [ "${ACTUAL_SHA256}" != "${EXPECTED_SHA256}" ]; then
    echo "  ERROR: SHA256 mismatch for ${ORT_PLATFORM}"
    echo "    expected: ${EXPECTED_SHA256}"
    echo "    actual:   ${ACTUAL_SHA256}"
    exit 1
  fi
  echo "  SHA256 verified: ${ACTUAL_SHA256}"

  # Extract archive.
  if [ "${ARCHIVE_EXT}" = "zip" ]; then
    unzip -q "${ARCHIVE_FILE}" -d "${TMP_DIR}"
  else
    tar -xzf "${ARCHIVE_FILE}" -C "${TMP_DIR}"
  fi

  mkdir -p "${DEST_DIR}"

  # Find and copy the shared library.
  if [ "${GOOS}" = "windows" ]; then
    LIB_FILE="$(find "${TMP_DIR}" -path "*/lib/*" -name "onnxruntime.dll" | head -1)"
  else
    LIB_FILE="$(find "${TMP_DIR}" -path "*/lib/*" \( \
      -name "libonnxruntime.${EXT}" -o \
      -name "libonnxruntime.${EXT}.*" -o \
      -name "libonnxruntime.*.${EXT}" \
    \) -type f | head -1)"
  fi

  if [ -z "${LIB_FILE}" ]; then
    echo "  ERROR: Could not find ${DEST_NAME} in downloaded archive"
    exit 1
  fi

  cp "${LIB_FILE}" "${DEST_DIR}/${DEST_NAME}"
  echo "  Installed: ${DEST_DIR}/${DEST_NAME}"

  rm -rf "${TMP_DIR}"
done

echo "All ONNX Runtime libraries downloaded successfully."
