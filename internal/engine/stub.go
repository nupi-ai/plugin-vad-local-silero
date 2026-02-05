package engine

import "fmt"

const (
	// StubToggleInterval is the number of frames after which the stub engine
	// toggles between speech and silence. At 20ms per frame, 50 frames = 1 second.
	StubToggleInterval = 50

	// StubConfidence is the fixed confidence value returned by the stub engine.
	StubConfidence float32 = 0.42

	// stubFrameDurationMs is the duration of each inference frame in milliseconds.
	stubFrameDurationMs = 20

	// stubSamplesPerFrame is the number of samples per 20ms frame at 16kHz.
	// ExpectedSampleRate * 0.020 = 320 samples = 640 bytes (s16le).
	stubSamplesPerFrame = int(ExpectedSampleRate) * stubFrameDurationMs / 1000
)

// StubEngine returns deterministic VAD results by alternating between speech
// and silence every StubToggleInterval frames. It does not process audio data.
//
// Unlike earlier versions, StubEngine now returns N results proportional to PCM
// length, matching Silero's behavior. This ensures consistent timing regardless
// of chunk size. Each 640 bytes (320 samples, 20ms) produces one result.
//
// This engine is for testing only — it ignores actual audio content.
type StubEngine struct {
	counter  int
	speaking bool
	// pcmBuf accumulates samples until a full frame is ready.
	pcmBuf int
}

// NewStubEngine creates a StubEngine starting in silence state.
func NewStubEngine() *StubEngine {
	return &StubEngine{}
}

// ProcessChunk returns one Result per 20ms frame contained in the PCM buffer.
// Partial frames are buffered for the next call. This matches Silero's behavior.
func (e *StubEngine) ProcessChunk(pcm []byte, sampleRate uint32) ([]Result, error) {
	// Validate inputs for consistency with SileroEngine.
	if sampleRate != ExpectedSampleRate {
		return nil, ErrWrongSampleRate
	}
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("stub: PCM buffer has odd length %d, expected even (s16le requires 2 bytes per sample)", len(pcm))
	}
	// Convert bytes to samples (2 bytes per sample for s16le).
	samples := len(pcm) / 2
	e.pcmBuf += samples

	var results []Result
	for e.pcmBuf >= stubSamplesPerFrame {
		e.pcmBuf -= stubSamplesPerFrame
		e.counter++
		if e.counter >= StubToggleInterval {
			e.counter = 0
			e.speaking = !e.speaking
		}
		results = append(results, Result{
			IsSpeech:   e.speaking,
			Confidence: StubConfidence,
		})
	}
	return results, nil
}

// Reset returns the engine to its initial state (silence, counter zero).
func (e *StubEngine) Reset() error {
	e.counter = 0
	e.speaking = false
	e.pcmBuf = 0
	return nil
}

// Close is a no-op for the stub engine.
func (e *StubEngine) Close() error {
	return nil
}

// FrameDurationMs returns 20 — the duration of each inference frame.
func (e *StubEngine) FrameDurationMs() int {
	return stubFrameDurationMs
}

// SetThreshold is a no-op for the stub engine (IsSpeech is toggle-based).
func (e *StubEngine) SetThreshold(_ float64) {}

// SampleRate returns ExpectedSampleRate (16000 Hz, matching Silero).
func (e *StubEngine) SampleRate() uint32 { return ExpectedSampleRate }
