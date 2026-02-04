package engine

// Result holds the output of a single VAD frame.
type Result struct {
	IsSpeech   bool
	Confidence float32
}

// Engine processes audio chunks and returns per-frame VAD results.
type Engine interface {
	// ProcessChunk receives a PCM audio chunk and returns a VAD result.
	ProcessChunk(pcm []byte, sampleRate uint32) (Result, error)
	// Reset clears internal state (e.g., between sessions).
	Reset() error
	// Close releases resources.
	Close() error
}
