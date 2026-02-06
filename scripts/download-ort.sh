#!/usr/bin/env bash
# Download ONNX Runtime shared library for the current platform.
# Usage: ./scripts/download-ort.sh [--no-verify] [version]
#
# Version compatibility: yalue/onnxruntime_go v1.25.0 requires ORT API v23,
# which is provided by ONNX Runtime >= 1.23.0.
#
# Note: This script only copies the main ORT library (libonnxruntime.so/dylib/dll).
# Provider libraries (libonnxruntime_providers_*) are NOT copied since Silero VAD
# uses CPU inference only. For GPU/CUDA support, copy providers manually.
#
# SHA256 checksums are pinned per platform+version for supply-chain safety.
# To add a new version, run with --no-verify once, then compute the hash
# and update get_expected_sha256() below.
set -euo pipefail

VERIFY=true
ORT_VERSION="1.23.0"
for arg in "$@"; do
  case "${arg}" in
    --no-verify) VERIFY=false ;;
    *) ORT_VERSION="${arg}" ;;
  esac
done

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

# Normalize OS name (handle Git Bash/MSYS/Cygwin on Windows).
case "${OS}" in
  mingw*|msys*|cygwin*) OS="windows" ;;
esac

case "${OS}" in
  darwin)
    case "${ARCH}" in
      arm64) PLATFORM="osx-arm64"; GOARCH="arm64"; EXT="dylib" ;;
      x86_64) PLATFORM="osx-x86_64"; GOARCH="amd64"; EXT="dylib" ;;
      *) echo "Unsupported macOS arch: ${ARCH}"; exit 1 ;;
    esac
    ;;
  linux)
    case "${ARCH}" in
      x86_64) PLATFORM="linux-x64"; GOARCH="amd64"; EXT="so" ;;
      aarch64|arm64) PLATFORM="linux-aarch64"; GOARCH="arm64"; EXT="so" ;;
      *) echo "Unsupported Linux arch: ${ARCH}"; exit 1 ;;
    esac
    ;;
  windows)
    case "${ARCH}" in
      x86_64|AMD64) PLATFORM="win-x64"; GOARCH="amd64"; EXT="dll" ;;
      aarch64|arm64|ARM64) PLATFORM="win-arm64"; GOARCH="arm64"; EXT="dll" ;;
      *) echo "Unsupported Windows arch: ${ARCH}"; exit 1 ;;
    esac
    ;;
  *)
    echo "Unsupported OS: ${OS}"; exit 1
    ;;
esac

# compute_sha256 <file> — portable SHA256 (macOS shasum / Linux sha256sum).
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

DEST_DIR="lib/${OS}-${GOARCH}"

# Windows uses .zip, others use .tgz.
if [ "${OS}" = "windows" ]; then
  ARCHIVE_EXT="zip"
else
  ARCHIVE_EXT="tgz"
fi
URL="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-${PLATFORM}-${ORT_VERSION}.${ARCHIVE_EXT}"

# Pinned SHA256 checksums for onnxruntime-<platform>-<version>.{tgz,zip} archives.
# NOTE: download-ort-all.sh duplicates checksums for its 4 target platforms.
# When updating, keep both files in sync.
get_expected_sha256() {
  case "${PLATFORM}:${ORT_VERSION}" in
    osx-arm64:1.23.0)     echo "8182db0ebb5caa21036a3c78178f17fabb98a7916bdab454467c8f4cf34bcfdf" ;;
    osx-x86_64:1.23.0)    echo "a8e43edcaa349cbfc51578a7fc61ea2b88793ccf077b4bc65aca58999d20cf0f" ;;
    linux-x64:1.23.0)     echo "b6deea7f2e22c10c043019f294a0ea4d2a6c0ae52a009c34847640db75ec5580" ;;
    linux-aarch64:1.23.0) echo "0b9f47d140411d938e47915824d8daaa424df95a88b5f1fc843172a75168f7a0" ;;
    win-x64:1.23.0)       echo "72c23470310ec79a7d42d27fe9d257e6c98540c73fa5a1db1f67f538c6c16f2f" ;;
    win-arm64:1.23.0)     echo "1c61071732e0b9e83c3ee4e42d8acea4acbd5ddb4dacd5e93a3ddf0ad4df590d" ;;
    *) echo "" ;;
  esac
}

echo "Downloading ONNX Runtime v${ORT_VERSION} for ${PLATFORM}..."
echo "URL: ${URL}"

TMP_DIR="$(mktemp -d)"
trap "rm -rf ${TMP_DIR}" EXIT INT TERM

ARCHIVE_FILE="${TMP_DIR}/ort.${ARCHIVE_EXT}"
curl -fsSL "${URL}" -o "${ARCHIVE_FILE}"

# Verify archive checksum.
if ${VERIFY}; then
  EXPECTED_SHA256="$(get_expected_sha256)"
  if [ -z "${EXPECTED_SHA256}" ]; then
    echo "ERROR: No pinned SHA256 for ${PLATFORM}:${ORT_VERSION}"
    echo "  Run with --no-verify to download without verification,"
    echo "  then add the hash to get_expected_sha256() in this script."
    echo "  Hash: $(compute_sha256 "${ARCHIVE_FILE}")"
    exit 1
  fi
  ACTUAL_SHA256="$(compute_sha256 "${ARCHIVE_FILE}")"
  if [ "${ACTUAL_SHA256}" != "${EXPECTED_SHA256}" ]; then
    echo "ERROR: SHA256 mismatch for ORT archive"
    echo "  expected: ${EXPECTED_SHA256}"
    echo "  actual:   ${ACTUAL_SHA256}"
    exit 1
  fi
  echo "SHA256 verified: ${ACTUAL_SHA256}"
fi

# Extract archive (zip for Windows, tgz for others).
if [ "${ARCHIVE_EXT}" = "zip" ]; then
  unzip -q "${ARCHIVE_FILE}" -d "${TMP_DIR}"
else
  tar -xzf "${ARCHIVE_FILE}" -C "${TMP_DIR}"
fi

mkdir -p "${DEST_DIR}"

# Find and copy the shared library from the lib/ subdirectory.
# Windows uses onnxruntime.dll, others use libonnxruntime.{so,dylib}.
# We search in */lib/* to avoid picking up wrong files from other locations.
#
# macOS archives may contain versioned dylibs in different naming schemes:
#   - libonnxruntime.dylib (symlink, may not exist)
#   - libonnxruntime.dylib.1.23.0 (version suffix)
#   - libonnxruntime.1.23.0.dylib (version in middle)
# We handle all cases with multiple -name patterns.
if [ "${OS}" = "windows" ]; then
  LIB_FILE="$(find "${TMP_DIR}" -path "*/lib/*" -name "onnxruntime.dll" | head -1)"
  DEST_NAME="onnxruntime.dll"
else
  # Try exact match first, then versioned patterns
  LIB_FILE="$(find "${TMP_DIR}" -path "*/lib/*" \( \
    -name "libonnxruntime.${EXT}" -o \
    -name "libonnxruntime.${EXT}.*" -o \
    -name "libonnxruntime.*.${EXT}" \
  \) -type f | head -1)"
  DEST_NAME="libonnxruntime.${EXT}"
fi

if [ -z "${LIB_FILE}" ]; then
  echo "ERROR: Could not find ${DEST_NAME} in downloaded archive"
  exit 1
fi

cp "${LIB_FILE}" "${DEST_DIR}/${DEST_NAME}"
echo "Installed: ${DEST_DIR}/${DEST_NAME}"
ls -lh "${DEST_DIR}/${DEST_NAME}"
