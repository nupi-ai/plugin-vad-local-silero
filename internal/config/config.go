package config

import (
	"fmt"
	"math"
	"strings"
)

const (
	DefaultListenAddr           = "localhost:0"
	DefaultThreshold            = 0.5
	DefaultMinSpeechDurationMs  = 250
	DefaultMinSilenceDurationMs = 300

	// MaxDurationMs is the upper bound for min_speech_duration_ms and
	// min_silence_duration_ms to prevent integer overflow in frame calculations.
	MaxDurationMs = 60000 // 1 minute
)

// Valid Engine values.
const (
	EngineSilero = "silero"
	EngineStub   = "stub"
)

// Config holds the adapter configuration.
//
// Note: speech_pad_ms (a common Silero VAD parameter for padding speech segments)
// is intentionally NOT implemented. This adapter uses MinSpeechDurationMs and
// MinSilenceDurationMs for boundary detection instead. If speech_pad_ms is set
// in config (env var or JSON), a warning will be logged at startup.
type Config struct {
	Engine               string  `json:"engine"`
	ListenAddr           string  `json:"listen_addr"`
	LogLevel             string  `json:"log_level"`
	Threshold            float64 `json:"threshold"`
	MinSpeechDurationMs  int     `json:"min_speech_duration_ms"`
	MinSilenceDurationMs int     `json:"min_silence_duration_ms"`
}

// Validate checks that all config values are within acceptable ranges.
// This is the full startup validation including ListenAddr.
// EngineAuto is a sentinel value indicating the engine should be auto-detected
// based on what's compiled into the binary.
const EngineAuto = "auto"

func (c *Config) Validate() error {
	c.Engine = strings.ToLower(strings.TrimSpace(c.Engine))
	if c.Engine == "" {
		c.Engine = EngineAuto
	}
	if c.Engine != EngineSilero && c.Engine != EngineStub && c.Engine != EngineAuto {
		return fmt.Errorf("config: engine must be %q, %q, or %q, got %q (set NUPI_VAD_ENGINE)", EngineSilero, EngineStub, EngineAuto, c.Engine)
	}
	c.ListenAddr = strings.TrimSpace(c.ListenAddr)
	if c.ListenAddr == "" {
		return fmt.Errorf("config: listen address is required")
	}
	return c.ValidateVADParams()
}

// ValidateVADParams checks that VAD-specific parameter values are within
// acceptable ranges. Used for both startup config and per-stream overrides.
func (c *Config) ValidateVADParams() error {
	if math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) {
		return fmt.Errorf("config: threshold must be a finite number, got %f", c.Threshold)
	}
	if c.Threshold < 0 || c.Threshold > 1.0 {
		return fmt.Errorf("config: threshold must be in [0.0, 1.0], got %f", c.Threshold)
	}
	if c.MinSpeechDurationMs <= 0 || c.MinSpeechDurationMs > MaxDurationMs {
		return fmt.Errorf("config: min_speech_duration_ms must be in (0, %d], got %d", MaxDurationMs, c.MinSpeechDurationMs)
	}
	if c.MinSilenceDurationMs <= 0 || c.MinSilenceDurationMs > MaxDurationMs {
		return fmt.Errorf("config: min_silence_duration_ms must be in (0, %d], got %d", MaxDurationMs, c.MinSilenceDurationMs)
	}
	return nil
}
