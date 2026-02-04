package config

import "fmt"

const (
	DefaultListenAddr          = "localhost:0"
	DefaultThreshold           = 0.5
	DefaultMinSpeechDurationMs = 250
	DefaultMinSilenceDurationMs = 300
	DefaultSpeechPadMs         = 30
)

// Config holds the adapter configuration.
type Config struct {
	ListenAddr          string  `json:"listen_addr"`
	LogLevel            string  `json:"log_level"`
	Threshold           float64 `json:"threshold"`
	MinSpeechDurationMs int     `json:"min_speech_duration_ms"`
	MinSilenceDurationMs int    `json:"min_silence_duration_ms"`
	SpeechPadMs         int     `json:"speech_pad_ms"`
}

// Validate checks that all config values are within acceptable ranges.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("config: listen address is required")
	}
	if c.Threshold <= 0 || c.Threshold > 1.0 {
		return fmt.Errorf("config: threshold must be in (0.0, 1.0], got %f", c.Threshold)
	}
	if c.MinSpeechDurationMs <= 0 {
		return fmt.Errorf("config: min_speech_duration_ms must be > 0, got %d", c.MinSpeechDurationMs)
	}
	if c.MinSilenceDurationMs <= 0 {
		return fmt.Errorf("config: min_silence_duration_ms must be > 0, got %d", c.MinSilenceDurationMs)
	}
	if c.SpeechPadMs < 0 {
		return fmt.Errorf("config: speech_pad_ms must be >= 0, got %d", c.SpeechPadMs)
	}
	return nil
}
