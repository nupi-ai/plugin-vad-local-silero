//go:build silero

package engine

// NativeAvailable reports that the Silero VAD engine is compiled in.
func NativeAvailable() bool { return true }

// NewNativeEngine creates a SileroEngine with the given speech threshold.
func NewNativeEngine(threshold float64) (Engine, error) {
	return NewSileroEngine(threshold)
}
