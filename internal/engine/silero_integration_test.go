//go:build silero

// IMPORTANT: Tests in this file use os.Chdir and MUST NOT use t.Parallel().
// The ORT library resolver depends on working directory, so tests must run
// sequentially to avoid race conditions.

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// projectRoot returns the absolute path to the project root.
func projectRoot(t *testing.T) string {
	t.Helper()
	// Tests in internal/engine/ → project root is 2 dirs up.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	root := filepath.Join(dir, "..", "..")
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("cannot locate project root (expected go.mod at %s)", root)
	}
	return root
}

// withProjectRootCwd temporarily changes working directory to the project root.
// ORT library resolver uses os.Getwd(), so tests must run from project root.
// Returns cleanup function. Tests using this must NOT run in parallel.
func withProjectRootCwd(t *testing.T) {
	t.Helper()
	root := projectRoot(t)

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("os.Chdir(%s): %v", root, err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// skipWithoutORT skips the test if the ORT library is not available.
func skipWithoutORT(t *testing.T) {
	t.Helper()
	withProjectRootCwd(t)
	// Enable CWD-based library lookup for tests.
	// t.Setenv automatically restores the original value on test cleanup.
	t.Setenv("NUPI_DEV_MODE", "1")
	if _, err := resolveORTLibPath(); err != nil {
		t.Skipf("ONNX Runtime library not found — run 'make download-ort': %v", err)
	}
}

func TestSileroEngine_Integration(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	// Generate 512 samples of silence (1024 bytes of s16le zeros).
	silence := make([]byte, sileroWindowSize*2)
	results, err := eng.ProcessChunk(silence, 16000)
	if err != nil {
		t.Fatalf("ProcessChunk silence: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Confidence > 0.5 {
		t.Errorf("silence confidence = %v, expected < 0.5", results[0].Confidence)
	}
	if results[0].IsSpeech {
		t.Error("silence should not be detected as speech")
	}
}

func TestSileroEngine_Reset_Integration(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	chunk := make([]byte, sileroWindowSize*2)
	for i := 0; i < 10; i++ {
		if _, err := eng.ProcessChunk(chunk, 16000); err != nil {
			t.Fatalf("ProcessChunk: %v", err)
		}
	}

	if err := eng.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	results, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatalf("ProcessChunk after reset: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after reset, got %d", len(results))
	}
	if results[0].Confidence < 0 || results[0].Confidence > 1 {
		t.Errorf("confidence after reset = %v, expected [0, 1]", results[0].Confidence)
	}
}

func TestSileroEngine_SmallChunks_Integration(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	// Feed 320-sample chunks (20ms at 16kHz = 640 bytes).
	chunk := make([]byte, 320*2)

	// First chunk (320 samples) — not enough for 512-sample window.
	r1, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatalf("ProcessChunk 1: %v", err)
	}
	if len(r1) != 0 {
		t.Errorf("expected 0 results before full window, got %d", len(r1))
	}

	// Second chunk (total 640 ≥ 512) — inference should run.
	r2, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatalf("ProcessChunk 2: %v", err)
	}
	if len(r2) != 1 {
		t.Fatalf("expected 1 result after full window, got %d", len(r2))
	}
	if r2[0].Confidence < 0 || r2[0].Confidence > 1 {
		t.Errorf("confidence = %v, expected [0, 1]", r2[0].Confidence)
	}
}

func TestSileroEngine_WrongSampleRate(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	chunk := make([]byte, sileroWindowSize*2)
	_, err = eng.ProcessChunk(chunk, 8000)
	if err == nil {
		t.Fatal("expected error for wrong sample rate, got nil")
	}
}

func TestSileroEngine_OddPCMLength(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	// Odd-length buffer: 1023 bytes is not valid s16le.
	oddChunk := make([]byte, 1023)
	_, err = eng.ProcessChunk(oddChunk, 16000)
	if err == nil {
		t.Fatal("expected error for odd-length PCM buffer, got nil")
	}
}

func TestSileroEngine_InferenceLatency(t *testing.T) {
	// AC2 requires inference in <1ms on a single CPU thread.
	// This test measures actual inference time over multiple runs.
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}
	defer eng.Close()

	// Warm up: first inference may include JIT/cache overhead.
	warmup := make([]byte, sileroWindowSize*2)
	if _, err := eng.ProcessChunk(warmup, 16000); err != nil {
		t.Fatalf("warmup ProcessChunk: %v", err)
	}

	const iterations = 50
	chunk := make([]byte, sileroWindowSize*2) // exactly one window
	var totalDuration time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		if _, err := eng.ProcessChunk(chunk, 16000); err != nil {
			t.Fatalf("ProcessChunk iteration %d: %v", i, err)
		}
		totalDuration += time.Since(start)
	}

	avgMs := float64(totalDuration.Microseconds()) / float64(iterations) / 1000.0
	t.Logf("average inference latency: %.3f ms (over %d iterations)", avgMs, iterations)

	if avgMs > 1.0 {
		t.Errorf("average inference latency %.3f ms exceeds AC2 requirement of <1ms", avgMs)
	}
}

func TestSileroEngine_DoubleClose(t *testing.T) {
	skipWithoutORT(t)

	eng, err := NewSileroEngine(0.5)
	if err != nil {
		t.Fatalf("NewSileroEngine: %v", err)
	}

	// First close should succeed.
	if err := eng.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close must not panic.
	if err := eng.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
