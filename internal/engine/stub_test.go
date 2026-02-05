package engine

import "testing"

// stubFrameBytes is the size of a 20ms PCM chunk at 16kHz mono s16le (640 bytes).
const stubFrameBytes = 640

func TestStubEngineAlternatesSpeechSilence(t *testing.T) {
	eng := NewStubEngine()
	chunk := make([]byte, stubFrameBytes) // 20ms frame

	// First StubToggleInterval-1 frames should be silence (counter increments
	// before check, so the toggle fires on frame #StubToggleInterval).
	for i := 0; i < StubToggleInterval-1; i++ {
		results, err := eng.ProcessChunk(chunk, 16000)
		if err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
		if len(results) != 1 {
			t.Fatalf("frame %d: expected 1 result, got %d", i, len(results))
		}
		if results[0].IsSpeech {
			t.Fatalf("frame %d: expected silence, got speech", i)
		}
		if results[0].Confidence != StubConfidence {
			t.Fatalf("frame %d: confidence = %v, want %v", i, results[0].Confidence, StubConfidence)
		}
	}

	// The StubToggleInterval-th frame toggles to speech.
	results, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].IsSpeech {
		t.Fatal("expected speech after toggle")
	}

	// Continue for another full interval to reach silence again.
	for i := 1; i < StubToggleInterval; i++ {
		if _, err := eng.ProcessChunk(chunk, 16000); err != nil {
			t.Fatalf("frame %d (speech): unexpected error: %v", i, err)
		}
	}
	results, err = eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].IsSpeech {
		t.Fatal("expected silence after second toggle")
	}
}

func TestStubEngineReset(t *testing.T) {
	eng := NewStubEngine()
	chunk := make([]byte, stubFrameBytes)

	// Advance past the first toggle.
	for i := 0; i <= StubToggleInterval; i++ {
		if _, err := eng.ProcessChunk(chunk, 16000); err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
	}
	results, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].IsSpeech {
		t.Fatal("expected speech before reset")
	}

	// Reset should return to initial silence state.
	if err := eng.Reset(); err != nil {
		t.Fatal(err)
	}
	results, err = eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].IsSpeech {
		t.Fatal("expected silence after reset")
	}
}

func TestStubEngineConfidence(t *testing.T) {
	eng := NewStubEngine()
	chunk := make([]byte, stubFrameBytes)
	results, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Confidence != StubConfidence {
		t.Fatalf("confidence = %v, want %v", results[0].Confidence, StubConfidence)
	}
}

func TestStubEngineMultipleFramesPerChunk(t *testing.T) {
	eng := NewStubEngine()

	// Send 3 frames worth of audio in one chunk.
	chunk := make([]byte, stubFrameBytes*3)
	results, err := eng.ProcessChunk(chunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results for 3-frame chunk, got %d", len(results))
	}
}

func TestStubEnginePartialFrameBuffered(t *testing.T) {
	eng := NewStubEngine()

	// Send half a frame — should buffer, return 0 results.
	halfChunk := make([]byte, stubFrameBytes/2)
	results, err := eng.ProcessChunk(halfChunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for half-frame, got %d", len(results))
	}

	// Send another half — now we have a full frame.
	results, err = eng.ProcessChunk(halfChunk, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after completing frame, got %d", len(results))
	}
}

func TestStubEngineEmptyChunk(t *testing.T) {
	eng := NewStubEngine()

	// Empty chunk should return 0 results.
	results, err := eng.ProcessChunk(nil, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for empty chunk, got %d", len(results))
	}

	results, err = eng.ProcessChunk([]byte{}, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for zero-length chunk, got %d", len(results))
	}
}

func TestStubEngineOddPCMLength(t *testing.T) {
	eng := NewStubEngine()

	// Odd-length buffer: 641 bytes is not valid s16le.
	oddChunk := make([]byte, 641)
	_, err := eng.ProcessChunk(oddChunk, 16000)
	if err == nil {
		t.Fatal("expected error for odd-length PCM buffer, got nil")
	}
}

func TestStubEngineFrameDurationMs(t *testing.T) {
	eng := NewStubEngine()
	if d := eng.FrameDurationMs(); d != 20 {
		t.Fatalf("FrameDurationMs() = %d, want 20", d)
	}
}

func TestStubEngineWrongSampleRate(t *testing.T) {
	eng := NewStubEngine()

	chunk := make([]byte, stubFrameBytes)
	_, err := eng.ProcessChunk(chunk, 8000)
	if err == nil {
		t.Fatal("expected error for wrong sample rate, got nil")
	}
	if err != ErrWrongSampleRate {
		t.Errorf("expected ErrWrongSampleRate, got: %v", err)
	}
}
