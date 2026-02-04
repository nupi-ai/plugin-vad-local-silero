package config

import "testing"

func TestLoaderDefaults(t *testing.T) {
	loader := Loader{
		Lookup: func(string) (string, bool) { return "", false },
	}
	cfg, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, DefaultListenAddr)
	}
	if cfg.Threshold != DefaultThreshold {
		t.Errorf("Threshold = %v, want %v", cfg.Threshold, DefaultThreshold)
	}
	if cfg.MinSpeechDurationMs != DefaultMinSpeechDurationMs {
		t.Errorf("MinSpeechDurationMs = %d, want %d", cfg.MinSpeechDurationMs, DefaultMinSpeechDurationMs)
	}
	if cfg.MinSilenceDurationMs != DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs = %d, want %d", cfg.MinSilenceDurationMs, DefaultMinSilenceDurationMs)
	}
	if cfg.SpeechPadMs != DefaultSpeechPadMs {
		t.Errorf("SpeechPadMs = %d, want %d", cfg.SpeechPadMs, DefaultSpeechPadMs)
	}
}

func TestLoaderJSON(t *testing.T) {
	env := map[string]string{
		"NUPI_ADAPTER_CONFIG": `{"threshold":0.7,"min_speech_duration_ms":100,"listen_addr":"localhost:9999"}`,
	}
	loader := Loader{
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
	if cfg.MinSilenceDurationMs != DefaultMinSilenceDurationMs {
		t.Errorf("MinSilenceDurationMs = %d, want default %d", cfg.MinSilenceDurationMs, DefaultMinSilenceDurationMs)
	}
}

func TestLoaderEnvOverride(t *testing.T) {
	env := map[string]string{
		"NUPI_ADAPTER_CONFIG":             `{"threshold":0.3}`,
		"NUPI_ADAPTER_LISTEN_ADDR":        "127.0.0.1:5555",
		"NUPI_VAD_THRESHOLD":              "0.8",
		"NUPI_VAD_MIN_SPEECH_DURATION_MS": "500",
	}
	loader := Loader{
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
	loader := Loader{
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
