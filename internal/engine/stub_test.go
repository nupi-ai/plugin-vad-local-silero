package engine

import "testing"

func TestStubEngineAlternatesSpeechSilence(t *testing.T) {
	eng := NewStubEngine()

	// First StubToggleInterval-1 chunks should be silence (counter increments
	// before check, so the toggle fires on call #StubToggleInterval).
	for i := 0; i < StubToggleInterval-1; i++ {
		r, err := eng.ProcessChunk(nil, 16000)
		if err != nil {
			t.Fatalf("chunk %d: unexpected error: %v", i, err)
		}
		if r.IsSpeech {
			t.Fatalf("chunk %d: expected silence, got speech", i)
		}
		if r.Confidence != StubConfidence {
			t.Fatalf("chunk %d: confidence = %v, want %v", i, r.Confidence, StubConfidence)
		}
	}

	// The StubToggleInterval-th call toggles to speech.
	r, err := eng.ProcessChunk(nil, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsSpeech {
		t.Fatal("expected speech after toggle")
	}

	// Continue for another full interval to reach silence again.
	for i := 1; i < StubToggleInterval; i++ {
		eng.ProcessChunk(nil, 16000)
	}
	r, err = eng.ProcessChunk(nil, 16000)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsSpeech {
		t.Fatal("expected silence after second toggle")
	}
}

func TestStubEngineReset(t *testing.T) {
	eng := NewStubEngine()

	// Advance past the first toggle.
	for i := 0; i <= StubToggleInterval; i++ {
		eng.ProcessChunk(nil, 16000)
	}
	r, _ := eng.ProcessChunk(nil, 16000)
	if !r.IsSpeech {
		t.Fatal("expected speech before reset")
	}

	// Reset should return to initial silence state.
	if err := eng.Reset(); err != nil {
		t.Fatal(err)
	}
	r, _ = eng.ProcessChunk(nil, 16000)
	if r.IsSpeech {
		t.Fatal("expected silence after reset")
	}
}

func TestStubEngineConfidence(t *testing.T) {
	eng := NewStubEngine()
	r, _ := eng.ProcessChunk(nil, 16000)
	if r.Confidence != StubConfidence {
		t.Fatalf("confidence = %v, want %v", r.Confidence, StubConfidence)
	}
}
