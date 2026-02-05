//go:build silero

package engine

import (
	"fmt"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	// sileroWindowSize is the number of float32 samples per inference call.
	// Silero VAD v5 at 16 kHz requires exactly 512 samples (32 ms).
	sileroWindowSize = 512

	// sileroStateSize is the hidden state dimension per layer.
	// Silero VAD v5 uses a combined state tensor of shape [2, 1, 128].
	sileroStateSize = 128
)

// ortInitOnce ensures ONNX Runtime environment is initialized exactly once.
// ortInitErr is stored at package scope so subsequent NewSileroEngine calls
// surface the failure instead of proceeding with an uninitialized environment.
var (
	ortInitOnce sync.Once
	ortInitErr  error
)

// SileroEngine runs Silero VAD v5 inference via ONNX Runtime.
type SileroEngine struct {
	session *ort.AdvancedSession

	// Input tensors (reused between calls).
	inputTensor *ort.Tensor[float32] // [1, 512]
	stateTensor *ort.Tensor[float32] // [2, 1, 128]
	srTensor    *ort.Tensor[int64]   // scalar

	// Output tensors (reused between calls).
	outputTensor *ort.Tensor[float32] // [1, 1]
	stateNTensor *ort.Tensor[float32] // [2, 1, 128]

	// PCM sample buffer for accumulating 20ms chunks to 512-sample windows.
	pcmBuf []float32

	threshold float64
}

// NewSileroEngine creates a SileroEngine by initializing ONNX Runtime,
// loading the embedded model, and allocating input/output tensors.
func NewSileroEngine(threshold float64) (*SileroEngine, error) {
	if len(sileroModelData) == 0 {
		return nil, fmt.Errorf("silero: model data is empty (build without silero tag?)")
	}

	ortInitOnce.Do(func() {
		libPath, err := resolveORTLibPath()
		if err != nil {
			ortInitErr = fmt.Errorf("resolve ORT lib: %w", err)
			return
		}
		ort.SetSharedLibraryPath(libPath)
		ortInitErr = ort.InitializeEnvironment()
	})
	if ortInitErr != nil {
		return nil, fmt.Errorf("silero: %w", ortInitErr)
	}

	// Allocate input tensors.
	inputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, sileroWindowSize))
	if err != nil {
		return nil, fmt.Errorf("silero: create input tensor: %w", err)
	}
	stateTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(2, 1, sileroStateSize))
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("silero: create state tensor: %w", err)
	}
	srTensor, err := ort.NewTensor(ort.NewShape(1), []int64{int64(ExpectedSampleRate)})
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		return nil, fmt.Errorf("silero: create sr tensor: %w", err)
	}

	// Allocate output tensors.
	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		return nil, fmt.Errorf("silero: create output tensor: %w", err)
	}
	stateNTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(2, 1, sileroStateSize))
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("silero: create stateN tensor: %w", err)
	}

	// Explicitly zero state tensors — onnxruntime_go may not guarantee zeroed memory.
	clearFloat32Slice(stateTensor.GetData())
	clearFloat32Slice(stateNTensor.GetData())

	// Create ONNX session from embedded model data.
	session, err := ort.NewAdvancedSessionWithONNXData(
		sileroModelData,
		[]string{"input", "state", "sr"},
		[]string{"output", "stateN"},
		[]ort.Value{inputTensor, stateTensor, srTensor},
		[]ort.Value{outputTensor, stateNTensor},
		nil, // default session options
	)
	if err != nil {
		inputTensor.Destroy()
		stateTensor.Destroy()
		srTensor.Destroy()
		outputTensor.Destroy()
		stateNTensor.Destroy()
		return nil, fmt.Errorf("silero: create session: %w", err)
	}

	return &SileroEngine{
		session:      session,
		inputTensor:  inputTensor,
		stateTensor:  stateTensor,
		srTensor:     srTensor,
		outputTensor: outputTensor,
		stateNTensor: stateNTensor,
		pcmBuf:       make([]float32, 0, sileroWindowSize*2),
		threshold:    threshold,
	}, nil
}

// ProcessChunk receives a PCM s16le audio chunk, buffers it, and runs
// inference for each complete 512-sample window. Returns one Result per
// inference, or an empty slice if not enough samples have accumulated.
func (e *SileroEngine) ProcessChunk(pcm []byte, sampleRate uint32) ([]Result, error) {
	if sampleRate != ExpectedSampleRate {
		return nil, ErrWrongSampleRate
	}
	if len(pcm)%2 != 0 {
		return nil, fmt.Errorf("silero: PCM buffer has odd length %d, expected even (s16le requires 2 bytes per sample)", len(pcm))
	}

	samples := pcmToFloat32(pcm)
	e.pcmBuf = append(e.pcmBuf, samples...)

	var results []Result
	for len(e.pcmBuf) >= sileroWindowSize {
		prob, err := e.infer(e.pcmBuf[:sileroWindowSize])
		if err != nil {
			return nil, err
		}
		e.pcmBuf = e.pcmBuf[sileroWindowSize:]
		results = append(results, Result{
			IsSpeech:   float64(prob) >= e.threshold,
			Confidence: prob,
		})
	}

	return results, nil
}

// SetThreshold updates the speech probability threshold.
func (e *SileroEngine) SetThreshold(threshold float64) {
	e.threshold = threshold
}

// Reset clears all internal state: RNN hidden states, PCM buffer.
func (e *SileroEngine) Reset() error {
	clearFloat32Slice(e.stateTensor.GetData())
	e.pcmBuf = e.pcmBuf[:0]
	return nil
}

// FrameDurationMs returns 32 — the Silero VAD window is 512 samples at 16kHz.
func (e *SileroEngine) FrameDurationMs() int {
	return int(sileroWindowSize * 1000 / ExpectedSampleRate) // 512 * 1000 / 16000 = 32
}

// SampleRate returns 16000 — Silero VAD requires 16 kHz input.
func (e *SileroEngine) SampleRate() uint32 { return ExpectedSampleRate }

// Close releases ONNX Runtime resources. Safe to call multiple times.
func (e *SileroEngine) Close() error {
	if e.session != nil {
		e.session.Destroy()
		e.session = nil
	}
	if e.inputTensor != nil {
		e.inputTensor.Destroy()
		e.inputTensor = nil
	}
	if e.stateTensor != nil {
		e.stateTensor.Destroy()
		e.stateTensor = nil
	}
	if e.srTensor != nil {
		e.srTensor.Destroy()
		e.srTensor = nil
	}
	if e.outputTensor != nil {
		e.outputTensor.Destroy()
		e.outputTensor = nil
	}
	if e.stateNTensor != nil {
		e.stateNTensor.Destroy()
		e.stateNTensor = nil
	}
	return nil
}

// infer runs a single Silero VAD inference on exactly 512 float32 samples.
func (e *SileroEngine) infer(window []float32) (float32, error) {
	// Copy window into input tensor.
	copy(e.inputTensor.GetData(), window)

	// Run inference.
	if err := e.session.Run(); err != nil {
		return 0, fmt.Errorf("silero: inference: %w", err)
	}

	// Read speech probability.
	prob := e.outputTensor.GetData()[0]

	// Carry forward hidden state: copy stateN → state.
	copy(e.stateTensor.GetData(), e.stateNTensor.GetData())

	return prob, nil
}

// pcmToFloat32 converts PCM s16le bytes to float32 samples normalized to [-1, 1].
// Divides by 32768 (not 32767) so that the full int16 range [-32768, 32767] maps
// to [-1.0, ~0.99997], keeping all values strictly within [-1, 1].
func pcmToFloat32(buf []byte) []float32 {
	n := len(buf) / 2
	if n == 0 {
		return nil
	}
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		u := uint16(buf[2*i]) | uint16(buf[2*i+1])<<8
		samples[i] = float32(int16(u)) / 32768.0
	}
	return samples
}

func clearFloat32Slice(s []float32) {
	for i := range s {
		s[i] = 0
	}
}
