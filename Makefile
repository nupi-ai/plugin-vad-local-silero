BINARY_NAME := vad-local-silero

# Pinned to Silero VAD v5.1 release (commit 84768ce, tag v5.1).
# SHA256 verifies model integrity; URL pinned to tag to avoid upstream breakage.
SILERO_MODEL_SHA256 := 1a153a22f4509e292a94e67d6f9b85e8deb25b4988682b7e174c65279d8788e3
SILERO_MODEL_URL := https://github.com/snakers4/silero-vad/raw/v5.1/src/silero_vad/data/silero_vad.onnx

# Portable SHA256 function for use in recipes.
# Usage: $(call sha256,filename) - outputs hash or fails with clear error.
define sha256
$(shell if command -v shasum >/dev/null 2>&1; then shasum -a 256 $(1) | awk '{print $$1}'; \
elif command -v sha256sum >/dev/null 2>&1; then sha256sum $(1) | awk '{print $$1}'; \
else echo "SHA256_TOOL_MISSING"; fi)
endef

# Check that SHA256 tool is available (called at start of recipes needing it).
define check_sha256_tool
@if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then \
	echo "ERROR: neither shasum nor sha256sum found. Install coreutils."; \
	exit 1; \
fi
endef

.PHONY: build build-stub clean test test-silero tidy download-ort download-ort-all download-model prepare-model release-snapshot release

# Production build with Silero VAD (requires model to be downloaded first).
build: prepare-model
	go build -tags silero -o $(BINARY_NAME) ./cmd/adapter/

# Development/test build without Silero (uses stub engine, no ONNX dependency).
build-stub:
	go build -o $(BINARY_NAME) ./cmd/adapter/

clean:
	rm -f $(BINARY_NAME)

# Run tests without silero (stub engine only, no ONNX dependency).
test:
	go test -race ./...

# Run tests with silero engine (requires ORT library + model).
# Use this in CI to catch regressions in silero-specific code paths.
test-silero: prepare-model
	go test -race -tags silero ./...

# IMPORTANT: Run "make tidy" instead of bare "go mod tidy" to preserve
# silero-only dependencies (onnxruntime_go). Bare tidy removes them.
tidy:
	GOFLAGS="-tags=silero" go mod tidy

download-ort:
	./scripts/download-ort.sh

# Download ONNX Runtime for all target platforms (used by GoReleaser).
download-ort-all:
	./scripts/download-ort-all.sh

# Build release archives locally (snapshot, no publish).
release-snapshot: download-model prepare-model
	goreleaser release --snapshot --clean --skip=publish

# Build and publish release (requires GITHUB_TOKEN and a git tag).
release: download-model prepare-model
	goreleaser release --clean

download-model:
	$(call check_sha256_tool)
	@mkdir -p models
	curl -fsSL -o models/silero_vad.onnx $(SILERO_MODEL_URL)
	@ACTUAL="$(call sha256,models/silero_vad.onnx)"; \
	if [ "$$ACTUAL" = "SHA256_TOOL_MISSING" ]; then \
		echo "ERROR: neither shasum nor sha256sum found. Install coreutils."; \
		rm -f models/silero_vad.onnx; \
		exit 1; \
	fi; \
	if [ "$$ACTUAL" != "$(SILERO_MODEL_SHA256)" ]; then \
		echo "ERROR: SHA256 mismatch for silero_vad.onnx"; \
		echo "  expected: $(SILERO_MODEL_SHA256)"; \
		echo "  actual:   $$ACTUAL"; \
		rm -f models/silero_vad.onnx; \
		exit 1; \
	fi
	@echo "SHA256 verified: $(SILERO_MODEL_SHA256)"

prepare-model:
	$(call check_sha256_tool)
	@if [ ! -f models/silero_vad.onnx ]; then \
		echo "ERROR: models/silero_vad.onnx not found. Download it first:"; \
		echo "  make download-model"; \
		exit 1; \
	fi
	@ACTUAL="$(call sha256,models/silero_vad.onnx)"; \
	if [ "$$ACTUAL" = "SHA256_TOOL_MISSING" ]; then \
		echo "ERROR: neither shasum nor sha256sum found. Install coreutils."; \
		exit 1; \
	fi; \
	if [ "$$ACTUAL" != "$(SILERO_MODEL_SHA256)" ]; then \
		echo "ERROR: SHA256 mismatch for models/silero_vad.onnx"; \
		echo "  expected: $(SILERO_MODEL_SHA256)"; \
		echo "  actual:   $$ACTUAL"; \
		exit 1; \
	fi
	cp models/silero_vad.onnx internal/engine/silero_vad.onnx
