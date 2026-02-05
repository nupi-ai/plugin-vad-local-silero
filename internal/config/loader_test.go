package config_test

import (
	"math"
	"strings"
	"testing"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
)

func TestLoaderDefaults(t *testing.T) {
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			if key == "NUPI_VAD_ENGINE" {
				return "silero", true
			}
			return "", false
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Config
	if cfg.Engine != config.EngineSilero {
		t.Errorf("Engine = %q, want %q", cfg.Engine, config.EngineSilero)
	}
	if cfg.ListenAddr != config.DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, config.DefaultListenAddr)
	}
	if cfg.Threshold != config.DefaultThreshold {
		t.Errorf("Threshold = %v, want %v", cfg.Threshold, config.DefaultThreshold)
	}
	if cfg.MinSpeechDurationMs != config.DefaultMinSpeechDurationMs {
		t.Errorf("MinSpeechDurationMs = %d, want %d", cfg.MinSpeechDurationMs, config.DefaultMinSpeechDurationMs)
	}
	if cfg.MinSilenceDurationMs != config.DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs = %d, want %d", cfg.MinSilenceDurationMs, config.DefaultMinSilenceDurationMs)
	}
}

func TestLoaderMissingEngineDefaultsToAuto(t *testing.T) {
	loader := config.Loader{
		Lookup: func(string) (string, bool) { return "", false },
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatalf("expected no error when NUPI_VAD_ENGINE is not set (should default to auto), got: %v", err)
	}
	cfg := result.Config
	if cfg.Engine != config.EngineAuto {
		t.Errorf("expected engine to default to %q, got %q", config.EngineAuto, cfg.Engine)
	}
}

func TestLoaderInvalidEngine(t *testing.T) {
	env := map[string]string{"NUPI_VAD_ENGINE": "unknown"}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid engine value")
	}
	if !strings.Contains(err.Error(), "engine") {
		t.Errorf("error should mention engine, got: %v", err)
	}
}

func TestLoaderJSON(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":     "stub",
		"NUPI_ADAPTER_CONFIG": `{"threshold":0.7,"min_speech_duration_ms":100,"listen_addr":"localhost:9999"}`,
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Config
	if cfg.Threshold != 0.7 {
		t.Errorf("Threshold = %v, want 0.7", cfg.Threshold)
	}
	if cfg.MinSpeechDurationMs != 100 {
		t.Errorf("MinSpeechDurationMs = %d, want 100", cfg.MinSpeechDurationMs)
	}
	if cfg.ListenAddr != "localhost:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "localhost:9999")
	}
	// Unset fields keep defaults.
	if cfg.MinSilenceDurationMs != config.DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs = %d, want default %d", cfg.MinSilenceDurationMs, config.DefaultMinSilenceDurationMs)
	}
}

func TestLoaderEnvOverride(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":                 "silero",
		"NUPI_ADAPTER_CONFIG":             `{"threshold":0.3}`,
		"NUPI_ADAPTER_LISTEN_ADDR":        "127.0.0.1:5555",
		"NUPI_VAD_THRESHOLD":              "0.8",
		"NUPI_VAD_MIN_SPEECH_DURATION_MS": "500",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Config
	// Env var overrides JSON.
	if cfg.Threshold != 0.8 {
		t.Errorf("Threshold = %v, want 0.8 (env override)", cfg.Threshold)
	}
	if cfg.ListenAddr != "127.0.0.1:5555" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:5555")
	}
	if cfg.MinSpeechDurationMs != 500 {
		t.Errorf("MinSpeechDurationMs = %d, want 500", cfg.MinSpeechDurationMs)
	}
}

func TestLoaderInvalidJSON(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":     "stub",
		"NUPI_ADAPTER_CONFIG": `{bad json}`,
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoaderEmptyEnvVarsSkipped(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":                  "stub",
		"NUPI_VAD_THRESHOLD":               "",
		"NUPI_VAD_MIN_SPEECH_DURATION_MS":  "  ",
		"NUPI_VAD_MIN_SILENCE_DURATION_MS": "",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatalf("empty env vars should be skipped, got: %v", err)
	}
	cfg := result.Config
	if cfg.Threshold != config.DefaultThreshold {
		t.Errorf("Threshold should keep default for empty env, got %v", cfg.Threshold)
	}
	if cfg.MinSpeechDurationMs != config.DefaultMinSpeechDurationMs {
		t.Errorf("MinSpeechDurationMs should keep default for empty env, got %d", cfg.MinSpeechDurationMs)
	}
	if cfg.MinSilenceDurationMs != config.DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs should keep default for empty env, got %d", cfg.MinSilenceDurationMs)
	}
}

func TestLoaderInvalidEnvFloat(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":    "stub",
		"NUPI_VAD_THRESHOLD": "abc",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid float env value")
	}
	if !strings.Contains(err.Error(), "NUPI_VAD_THRESHOLD") {
		t.Errorf("error should mention env var name, got: %v", err)
	}
}

func TestLoaderInvalidEnvInt(t *testing.T) {
	keys := []string{
		"NUPI_VAD_MIN_SPEECH_DURATION_MS",
		"NUPI_VAD_MIN_SILENCE_DURATION_MS",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			env := map[string]string{"NUPI_VAD_ENGINE": "stub", key: "not_a_number"}
			loader := config.Loader{
				Lookup: func(k string) (string, bool) {
					v, ok := env[k]
					return v, ok
				},
			}
			_, err := loader.Load()
			if err == nil {
				t.Fatalf("expected error for invalid %s", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error should mention %s, got: %v", key, err)
			}
		})
	}
}

func TestLoaderValidationThresholdOutOfRange(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"threshold_negative", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_THRESHOLD": "-0.5"}},
		{"threshold_above_one", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_THRESHOLD": "1.5"}},
		{"threshold_nan", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_THRESHOLD": "NaN"}},
		{"threshold_inf", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_THRESHOLD": "Inf"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := config.Loader{
				Lookup: func(key string) (string, bool) {
					v, ok := tt.env[key]
					return v, ok
				},
			}
			_, err := loader.Load()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), "threshold") {
				t.Errorf("error should mention threshold, got: %v", err)
			}
		})
	}
}

func TestLoaderValidationNegativeDuration(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"min_speech_zero", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_MIN_SPEECH_DURATION_MS": "0"}, "min_speech_duration_ms"},
		{"min_silence_negative", map[string]string{"NUPI_VAD_ENGINE": "stub", "NUPI_VAD_MIN_SILENCE_DURATION_MS": "-1"}, "min_silence_duration_ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := config.Loader{
				Lookup: func(key string) (string, bool) {
					v, ok := tt.env[key]
					return v, ok
				},
			}
			_, err := loader.Load()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error should mention %s, got: %v", tt.want, err)
			}
		})
	}
}

func TestLoaderValidationThresholdExactlyOne(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":    "stub",
		"NUPI_VAD_THRESHOLD": "1.0",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err != nil {
		t.Fatalf("threshold=1.0 should be valid, got: %v", err)
	}
}

func TestValidateEmptyListenAddr(t *testing.T) {
	cfg := config.Config{
		Engine:               config.EngineStub,
		ListenAddr:           "",
		Threshold:            config.DefaultThreshold,
		MinSpeechDurationMs:  config.DefaultMinSpeechDurationMs,
		MinSilenceDurationMs: config.DefaultMinSilenceDurationMs,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty listen address")
	}
	if !strings.Contains(err.Error(), "listen address") {
		t.Errorf("error should mention listen address, got: %v", err)
	}
}

func TestValidateDirectRangeChecks(t *testing.T) {
	valid := config.Config{
		Engine:               config.EngineStub,
		ListenAddr:           "localhost:0",
		Threshold:            0.5,
		MinSpeechDurationMs:  250,
		MinSilenceDurationMs: 300,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config should pass, got: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"threshold_negative", func(c *config.Config) { c.Threshold = -0.1 }, "threshold"},
		{"threshold_above_one", func(c *config.Config) { c.Threshold = 1.01 }, "threshold"},
		{"threshold_nan", func(c *config.Config) { c.Threshold = math.NaN() }, "threshold"},
		{"threshold_inf", func(c *config.Config) { c.Threshold = math.Inf(1) }, "threshold"},
		{"min_speech_zero", func(c *config.Config) { c.MinSpeechDurationMs = 0 }, "min_speech_duration_ms"},
		{"min_silence_negative", func(c *config.Config) { c.MinSilenceDurationMs = -1 }, "min_silence_duration_ms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error should mention %s, got: %v", tt.want, err)
			}
		})
	}
}

func TestLoaderValidationThresholdZeroValid(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":    "stub",
		"NUPI_VAD_THRESHOLD": "0",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err != nil {
		t.Fatalf("threshold=0.0 should be valid, got: %v", err)
	}
}

func TestValidateWhitespaceListenAddr(t *testing.T) {
	cfg := config.Config{
		Engine:               config.EngineStub,
		ListenAddr:           "  ",
		Threshold:            config.DefaultThreshold,
		MinSpeechDurationMs:  config.DefaultMinSpeechDurationMs,
		MinSilenceDurationMs: config.DefaultMinSilenceDurationMs,
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for whitespace-only listen address")
	}
	if !strings.Contains(err.Error(), "listen address") {
		t.Errorf("error should mention listen address, got: %v", err)
	}
}

func TestValidateListenAddrFromJSON(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_ENGINE":     "stub",
		"NUPI_ADAPTER_CONFIG": `{"listen_addr": "  "}`,
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for whitespace listen_addr in JSON")
	}
	if !strings.Contains(err.Error(), "listen address") {
		t.Errorf("error should mention listen address, got: %v", err)
	}
}

func TestLoaderWarnsSpeechPadMsEnv(t *testing.T) {
	// Verify that NUPI_VAD_SPEECH_PAD_MS env var generates a warning.
	env := map[string]string{
		"NUPI_VAD_ENGINE":        "stub",
		"NUPI_VAD_SPEECH_PAD_MS": "100",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning for NUPI_VAD_SPEECH_PAD_MS")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "NUPI_VAD_SPEECH_PAD_MS") && strings.Contains(w, "not supported") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about NUPI_VAD_SPEECH_PAD_MS, got: %v", result.Warnings)
	}
}

func TestLoaderWarnsSpeechPadMsJSON(t *testing.T) {
	// Verify that speech_pad_ms in JSON config generates a warning.
	env := map[string]string{
		"NUPI_VAD_ENGINE":     "stub",
		"NUPI_ADAPTER_CONFIG": `{"speech_pad_ms": 100}`,
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	result, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning for speech_pad_ms in JSON")
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "speech_pad_ms") && strings.Contains(w, "not supported") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about speech_pad_ms, got: %v", result.Warnings)
	}
}
