package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Loader loads configuration from environment variables. Tests can override
// Lookup to inject deterministic maps.
type Loader struct {
	Lookup func(string) (string, bool)
}

// LoadResult contains the loaded configuration and any warnings.
type LoadResult struct {
	Config   Config
	Warnings []string
}

// Load retrieves the adapter configuration from environment variables.
// Returns LoadResult containing config and warnings for deprecated parameters.
func (l Loader) Load() (LoadResult, error) {
	if l.Lookup == nil {
		l.Lookup = os.LookupEnv
	}

	cfg := Config{
		ListenAddr:           DefaultListenAddr,
		Threshold:            DefaultThreshold,
		MinSpeechDurationMs:  DefaultMinSpeechDurationMs,
		MinSilenceDurationMs: DefaultMinSilenceDurationMs,
	}

	var warnings []string

	if raw, ok := l.Lookup("NUPI_ADAPTER_CONFIG"); ok && strings.TrimSpace(raw) != "" {
		jsonWarnings, err := applyJSON(raw, &cfg)
		if err != nil {
			return LoadResult{}, err
		}
		warnings = append(warnings, jsonWarnings...)
	}

	// Warn about unsupported speech_pad_ms environment variable.
	if _, ok := l.Lookup("NUPI_VAD_SPEECH_PAD_MS"); ok {
		warnings = append(warnings, "NUPI_VAD_SPEECH_PAD_MS is not supported and will be ignored; use min_speech_duration_ms and min_silence_duration_ms instead")
	}

	overrideString(l.Lookup, "NUPI_VAD_ENGINE", &cfg.Engine)
	overrideString(l.Lookup, "NUPI_ADAPTER_LISTEN_ADDR", &cfg.ListenAddr)
	overrideString(l.Lookup, "NUPI_LOG_LEVEL", &cfg.LogLevel)
	if err := overrideFloat(l.Lookup, "NUPI_VAD_THRESHOLD", &cfg.Threshold); err != nil {
		return LoadResult{}, err
	}
	if err := overrideInt(l.Lookup, "NUPI_VAD_MIN_SPEECH_DURATION_MS", &cfg.MinSpeechDurationMs); err != nil {
		return LoadResult{}, err
	}
	if err := overrideInt(l.Lookup, "NUPI_VAD_MIN_SILENCE_DURATION_MS", &cfg.MinSilenceDurationMs); err != nil {
		return LoadResult{}, err
	}

	if err := cfg.Validate(); err != nil {
		return LoadResult{}, err
	}
	return LoadResult{Config: cfg, Warnings: warnings}, nil
}

func applyJSON(raw string, cfg *Config) ([]string, error) {
	// Include speech_pad_ms in struct to detect if it was set.
	type jsonConfig struct {
		Engine               string   `json:"engine"`
		ListenAddr           string   `json:"listen_addr"`
		LogLevel             string   `json:"log_level"`
		Threshold            *float64 `json:"threshold"`
		MinSpeechDurationMs  *int     `json:"min_speech_duration_ms"`
		MinSilenceDurationMs *int     `json:"min_silence_duration_ms"`
		SpeechPadMs          *int     `json:"speech_pad_ms"` // unsupported, for warning only
	}
	var payload jsonConfig
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("config: decode NUPI_ADAPTER_CONFIG: %w", err)
	}

	var warnings []string
	if payload.SpeechPadMs != nil {
		warnings = append(warnings, "speech_pad_ms in NUPI_ADAPTER_CONFIG is not supported and will be ignored; use min_speech_duration_ms and min_silence_duration_ms instead")
	}

	if payload.Engine != "" {
		cfg.Engine = payload.Engine
	}
	if payload.ListenAddr != "" {
		cfg.ListenAddr = payload.ListenAddr
	}
	if payload.LogLevel != "" {
		cfg.LogLevel = payload.LogLevel
	}
	if payload.Threshold != nil {
		cfg.Threshold = *payload.Threshold
	}
	if payload.MinSpeechDurationMs != nil {
		cfg.MinSpeechDurationMs = *payload.MinSpeechDurationMs
	}
	if payload.MinSilenceDurationMs != nil {
		cfg.MinSilenceDurationMs = *payload.MinSilenceDurationMs
	}
	return warnings, nil
}

func overrideString(lookup func(string) (string, bool), key string, target *string) {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		*target = strings.TrimSpace(value)
	}
}

func overrideFloat(lookup func(string) (string, bool), key string, target *float64) error {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return fmt.Errorf("config: invalid value for %s: %w", key, err)
		}
		*target = parsed
	}
	return nil
}

func overrideInt(lookup func(string) (string, bool), key string, target *int) error {
	if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("config: invalid value for %s: %w", key, err)
		}
		*target = parsed
	}
	return nil
}
