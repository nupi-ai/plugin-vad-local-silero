package config

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
