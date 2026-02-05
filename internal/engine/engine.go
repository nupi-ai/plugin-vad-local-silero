package engine

import "fmt"

// ExpectedSampleRate is the audio sample rate (Hz) required by all VAD engines.
// Both SileroEngine and StubEngine require 16kHz mono audio.
const ExpectedSampleRate uint32 = 16000

// ErrWrongSampleRate is returned when audio has an unsupported sample rate.
var ErrWrongSampleRate = fmt.Errorf("unsupported sample rate, expected %d Hz", ExpectedSampleRate)

// Result holds the output of a single VAD inference frame.
type Result struct {
	IsSpeech   bool
	Confidence float32
}

// Engine processes audio chunks and returns per-frame VAD results.
type Engine interface {
	// ProcessChunk receives a PCM audio chunk and returns zero or more
	// VAD results. An empty slice means the engine buffered samples but
	// did not have enough for inference. Multiple results are returned
	// when the chunk contains more audio than one inference window.
	ProcessChunk(pcm []byte, sampleRate uint32) ([]Result, error)
	// Reset clears internal state (e.g., between sessions).
	Reset() error
	// Close releases resources.
	Close() error
	// FrameDurationMs returns the effective audio duration (in ms) covered
	// by each inferred result. Used by the boundary detector for timing.
	FrameDurationMs() int
	// SetThreshold updates the speech probability threshold used to
	// determine IsSpeech. This allows per-stream threshold overrides.
	SetThreshold(threshold float64)
	// SampleRate returns the audio sample rate (Hz) the engine expects.
	SampleRate() uint32
}
