package config_test

import (
	"strings"
	"testing"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
)

func TestLoaderDefaults(t *testing.T) {
	loader := config.Loader{
		Lookup: func(string) (string, bool) { return "", false },
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
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
	if cfg.SpeechPadMs != config.DefaultSpeechPadMs {
		t.Errorf("SpeechPadMs = %d, want %d", cfg.SpeechPadMs, config.DefaultSpeechPadMs)
	}
}

func TestLoaderJSON(t *testing.T) {
	env := map[string]string{
		"NUPI_ADAPTER_CONFIG": `{"threshold":0.7,"min_speech_duration_ms":100,"listen_addr":"localhost:9999"}`,
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
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
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
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
		"NUPI_VAD_THRESHOLD":               "",
		"NUPI_VAD_MIN_SPEECH_DURATION_MS":  "  ",
		"NUPI_VAD_MIN_SILENCE_DURATION_MS": "",
		"NUPI_VAD_SPEECH_PAD_MS":           "",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("empty env vars should be skipped, got: %v", err)
	}
	if cfg.Threshold != config.DefaultThreshold {
		t.Errorf("Threshold should keep default for empty env, got %v", cfg.Threshold)
	}
	if cfg.MinSpeechDurationMs != config.DefaultMinSpeechDurationMs {
		t.Errorf("MinSpeechDurationMs should keep default for empty env, got %d", cfg.MinSpeechDurationMs)
	}
	if cfg.MinSilenceDurationMs != config.DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs should keep default for empty env, got %d", cfg.MinSilenceDurationMs)
	}
	if cfg.SpeechPadMs != config.DefaultSpeechPadMs {
		t.Errorf("SpeechPadMs should keep default for empty env, got %d", cfg.SpeechPadMs)
	}
}

func TestLoaderInvalidEnvFloat(t *testing.T) {
	env := map[string]string{
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
		"NUPI_VAD_SPEECH_PAD_MS",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			env := map[string]string{key: "not_a_number"}
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
		{"threshold_zero", map[string]string{"NUPI_VAD_THRESHOLD": "0"}},
		{"threshold_negative", map[string]string{"NUPI_VAD_THRESHOLD": "-0.5"}},
		{"threshold_above_one", map[string]string{"NUPI_VAD_THRESHOLD": "1.5"}},
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
		{"min_speech_zero", map[string]string{"NUPI_VAD_MIN_SPEECH_DURATION_MS": "0"}, "min_speech_duration_ms"},
		{"min_silence_negative", map[string]string{"NUPI_VAD_MIN_SILENCE_DURATION_MS": "-1"}, "min_silence_duration_ms"},
		{"speech_pad_negative", map[string]string{"NUPI_VAD_SPEECH_PAD_MS": "-10"}, "speech_pad_ms"},
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

func TestLoaderValidationSpeechPadZero(t *testing.T) {
	env := map[string]string{
		"NUPI_VAD_SPEECH_PAD_MS": "0",
	}
	loader := config.Loader{
		Lookup: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
	}
	_, err := loader.Load()
	if err != nil {
		t.Fatalf("speech_pad_ms=0 should be valid, got: %v", err)
	}
}

func TestValidateEmptyListenAddr(t *testing.T) {
	cfg := config.Config{
		ListenAddr:           "",
		Threshold:            config.DefaultThreshold,
		MinSpeechDurationMs:  config.DefaultMinSpeechDurationMs,
		MinSilenceDurationMs: config.DefaultMinSilenceDurationMs,
		SpeechPadMs:          config.DefaultSpeechPadMs,
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
		ListenAddr:           "localhost:0",
		Threshold:            0.5,
		MinSpeechDurationMs:  250,
		MinSilenceDurationMs: 300,
		SpeechPadMs:          30,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config should pass, got: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"threshold_zero", func(c *config.Config) { c.Threshold = 0 }, "threshold"},
		{"threshold_negative", func(c *config.Config) { c.Threshold = -0.1 }, "threshold"},
		{"threshold_above_one", func(c *config.Config) { c.Threshold = 1.01 }, "threshold"},
		{"min_speech_zero", func(c *config.Config) { c.MinSpeechDurationMs = 0 }, "min_speech_duration_ms"},
		{"min_silence_negative", func(c *config.Config) { c.MinSilenceDurationMs = -1 }, "min_silence_duration_ms"},
		{"speech_pad_negative", func(c *config.Config) { c.SpeechPadMs = -1 }, "speech_pad_ms"},
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
