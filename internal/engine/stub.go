package engine

// StubToggleInterval is the number of chunks after which the stub engine
// toggles between speech and silence. At 20ms per chunk, 50 chunks = 1 second.
const StubToggleInterval = 50

// StubConfidence is the fixed confidence value returned by the stub engine.
const StubConfidence float32 = 0.42

// StubEngine returns deterministic VAD results by alternating between speech
// and silence every StubToggleInterval chunks. It does not process audio data.
type StubEngine struct {
	counter  int
	speaking bool
}

// NewStubEngine creates a StubEngine starting in silence state.
func NewStubEngine() *StubEngine {
	return &StubEngine{}
}

// ProcessChunk ignores PCM data and returns a deterministic result based on
// an internal counter that toggles speech/silence every StubToggleInterval chunks.
func (e *StubEngine) ProcessChunk(_ []byte, _ uint32) (Result, error) {
	e.counter++
	if e.counter >= StubToggleInterval {
		e.counter = 0
		e.speaking = !e.speaking
	}
	return Result{
		IsSpeech:   e.speaking,
		Confidence: StubConfidence,
	}, nil
}

// Reset returns the engine to its initial state (silence, counter zero).
func (e *StubEngine) Reset() error {
	e.counter = 0
	e.speaking = false
	return nil
}

// Close is a no-op for the stub engine.
func (e *StubEngine) Close() error {
	return nil
}
