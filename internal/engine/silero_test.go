//go:build silero

package engine

import (
	"runtime"
	"testing"
)

func TestPcmToFloat32_Empty(t *testing.T) {
	samples := pcmToFloat32(nil)
	if samples != nil {
		t.Fatalf("expected nil, got %v", samples)
	}
	samples = pcmToFloat32([]byte{})
	if samples != nil {
		t.Fatalf("expected nil for empty slice, got %v", samples)
	}
}

func TestPcmToFloat32_SingleByte(t *testing.T) {
	// Single byte is not enough for one s16le sample.
	samples := pcmToFloat32([]byte{0x01})
	if samples != nil {
		t.Fatalf("expected nil for odd byte count, got %v", samples)
	}
}

func TestPcmToFloat32_Silence(t *testing.T) {
	// Two zero bytes = one silent sample.
	samples := pcmToFloat32([]byte{0x00, 0x00})
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	if samples[0] != 0 {
		t.Fatalf("expected 0.0 for silence, got %v", samples[0])
	}
}

func TestPcmToFloat32_MaxPositive(t *testing.T) {
	// int16 max = 0x7FFF = 32767 → little-endian: 0xFF, 0x7F
	// Divided by 32768 → ~0.99997
	samples := pcmToFloat32([]byte{0xFF, 0x7F})
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	expected := float32(32767) / 32768.0
	if samples[0] != expected {
		t.Fatalf("expected %v, got %v", expected, samples[0])
	}
}

func TestPcmToFloat32_MaxNegative(t *testing.T) {
	// int16 min = -32768 = 0x8000 → little-endian: 0x00, 0x80
	// Divided by 32768 → exactly -1.0
	samples := pcmToFloat32([]byte{0x00, 0x80})
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
	expected := float32(-32768) / 32768.0 // -1.0
	if samples[0] != expected {
		t.Fatalf("expected %v, got %v", expected, samples[0])
	}
}

func TestPcmToFloat32_MultipleSamples(t *testing.T) {
	// Two samples: 0x0100 (256) and 0xFEFF (-257 as int16, LE: 0xFF, 0xFE)
	pcm := []byte{0x00, 0x01, 0xFF, 0xFE}
	samples := pcmToFloat32(pcm)
	if len(samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(samples))
	}
	if samples[0] != float32(256)/32768.0 {
		t.Fatalf("sample[0] = %v, want %v", samples[0], float32(256)/32768.0)
	}
	if samples[1] != float32(-257)/32768.0 {
		t.Fatalf("sample[1] = %v, want %v", samples[1], float32(-257)/32768.0)
	}
}

func TestClearFloat32Slice(t *testing.T) {
	s := []float32{1.0, 2.0, 3.0, 4.0, 5.0}
	clearFloat32Slice(s)
	for i, v := range s {
		if v != 0 {
			t.Fatalf("s[%d] = %v, want 0", i, v)
		}
	}
}

func TestClearFloat32Slice_Empty(t *testing.T) {
	// Should not panic.
	clearFloat32Slice(nil)
	clearFloat32Slice([]float32{})
}

func TestOrtLibFilename(t *testing.T) {
	name := ortLibFilename()
	switch runtime.GOOS {
	case "darwin":
		if name != "libonnxruntime.dylib" {
			t.Fatalf("expected libonnxruntime.dylib, got %s", name)
		}
	case "windows":
		if name != "onnxruntime.dll" {
			t.Fatalf("expected onnxruntime.dll, got %s", name)
		}
	default:
		if name != "libonnxruntime.so" {
			t.Fatalf("expected libonnxruntime.so, got %s", name)
		}
	}
}

func TestSileroConstants(t *testing.T) {
	if sileroWindowSize != 512 {
		t.Fatalf("sileroWindowSize = %d, want 512", sileroWindowSize)
	}
	if sileroStateSize != 128 {
		t.Fatalf("sileroStateSize = %d, want 128", sileroStateSize)
	}
	// ExpectedSampleRate is now the canonical constant (engine.go).
	if ExpectedSampleRate != 16000 {
		t.Fatalf("ExpectedSampleRate = %d, want 16000", ExpectedSampleRate)
	}
}

func TestModelDataNotEmpty(t *testing.T) {
	if len(sileroModelData) == 0 {
		t.Fatal("sileroModelData is empty — model not embedded")
	}
}

func TestNativeAvailable(t *testing.T) {
	if !NativeAvailable() {
		t.Fatal("NativeAvailable() should return true when built with silero tag")
	}
}

func TestSileroFrameDurationMs(t *testing.T) {
	// Verify the constant: 512 samples at 16kHz = 32ms.
	eng := &SileroEngine{}
	if d := eng.FrameDurationMs(); d != 32 {
		t.Fatalf("FrameDurationMs() = %d, want 32", d)
	}
}
