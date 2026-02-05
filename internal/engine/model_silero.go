//go:build silero

package engine

import (
	_ "embed"
)

// sileroModelData contains the Silero VAD v5 ONNX model embedded at build time.
//
// BUILD REQUIREMENT: The model file must exist at internal/engine/silero_vad.onnx
// before compiling with -tags silero. Run these commands in order:
//
//	make download-model   # download model to models/ (one-time, ~2MB)
//	make build            # prepare-model + compile with -tags silero
//
// If you see "pattern silero_vad.onnx: no matching files found" during build,
// it means the model file is missing. Run "make download-model" first.
//
//go:embed silero_vad.onnx
var sileroModelData []byte
