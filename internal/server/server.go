package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	napv1 "github.com/nupi-ai/nupi/api/nap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
	"github.com/nupi-ai/plugin-vad-local-silero/internal/engine"
)

// Server implements napv1.VoiceActivityDetectionServiceServer.
// Each DetectSpeech stream gets its own engine instance and config copy,
// so concurrent streams are fully isolated.
type Server struct {
	napv1.UnimplementedVoiceActivityDetectionServiceServer

	cfg       config.Config
	log       *slog.Logger
	newEngine func() engine.Engine
}

// New returns a new Server instance. The newEngine factory is called once per
// stream to create an isolated engine instance.
func New(cfg config.Config, logger *slog.Logger, newEngine func() engine.Engine) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:       cfg,
		log:       logger.With("component", "server"),
		newEngine: newEngine,
	}
}

// DetectSpeech implements the bidirectional streaming RPC. It receives audio
// chunks, feeds them to the engine, and applies speech boundary detection to
// emit START/END/ONGOING events.
func (s *Server) DetectSpeech(stream napv1.VoiceActivityDetectionService_DetectSpeechServer) error {
	// Per-stream state: own config copy + own engine instance.
	streamCfg := s.cfg
	eng := s.newEngine()
	defer eng.Close()

	var (
		initDone bool
		bd       *boundaryDetector
	)

	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Client closed the stream — flush any pending speech end.
				if bd != nil && bd.inSpeech {
					if sendErr := stream.Send(&napv1.SpeechEvent{
						Type:       napv1.SpeechEventType_SPEECH_EVENT_TYPE_END,
						Confidence: bd.lastConfidence,
						Timestamp:  timestamppb.Now(),
					}); sendErr != nil {
						return sendErr
					}
				}
				return nil
			}
			return err
		}
		if req == nil {
			continue
		}

		// On first request, apply per-stream config overrides.
		if !initDone {
			applyStreamConfig(s.log, req.GetConfigJson(), &streamCfg)
			bd = newBoundaryDetector(streamCfg)
			s.log.Info("stream opened",
				"session_id", req.GetSessionId(),
				"stream_id", req.GetStreamId(),
			)
			initDone = true
		}

		result, err := eng.ProcessChunk(req.GetPcmData(), req.GetFormat().GetSampleRate())
		if err != nil {
			s.log.Error("engine error", "error", err)
			return err
		}

		events := bd.process(result)
		for _, evt := range events {
			evt.Timestamp = timestamppb.Now()
			if sendErr := stream.Send(evt); sendErr != nil {
				return sendErr
			}
		}
	}
}

// applyStreamConfig parses optional JSON config from the first request and
// overrides relevant fields in the per-stream config copy.
func applyStreamConfig(log *slog.Logger, configJSON string, cfg *config.Config) {
	if strings.TrimSpace(configJSON) == "" {
		return
	}
	type streamCfg struct {
		Threshold            *float64 `json:"threshold"`
		MinSpeechDurationMs  *int     `json:"min_speech_duration_ms"`
		MinSilenceDurationMs *int     `json:"min_silence_duration_ms"`
		SpeechPadMs          *int     `json:"speech_pad_ms"`
	}
	var sc streamCfg
	if err := json.Unmarshal([]byte(configJSON), &sc); err != nil {
		log.Warn("failed to parse stream config_json", "error", err)
		return
	}
	if sc.Threshold != nil {
		cfg.Threshold = *sc.Threshold
	}
	if sc.MinSpeechDurationMs != nil {
		cfg.MinSpeechDurationMs = *sc.MinSpeechDurationMs
	}
	if sc.MinSilenceDurationMs != nil {
		cfg.MinSilenceDurationMs = *sc.MinSilenceDurationMs
	}
	if sc.SpeechPadMs != nil {
		cfg.SpeechPadMs = *sc.SpeechPadMs
	}
}

// boundaryDetector applies hysteresis to raw per-frame engine results,
// emitting speech events only after sustained speech/silence thresholds.
//
// NOTE: threshold is applied inside the engine (Engine.ProcessChunk returns
// IsSpeech already thresholded). speech_pad_ms requires buffered lookahead
// and will be implemented with the real Silero engine in Story 2.4.
//
// Chunk duration is assumed to be 20ms, which matches the Silero VAD model's
// fixed window size. If variable chunk sizes are needed, compute duration from
// len(pcm)/(sampleRate*bytesPerSample) instead.
type boundaryDetector struct {
	inSpeech       bool
	speechFrames   int
	silenceFrames  int
	lastConfidence float32

	// Derived from config: number of consecutive frames needed.
	minSpeechFrames  int
	minSilenceFrames int
}

func newBoundaryDetector(cfg config.Config) *boundaryDetector {
	const chunkMs = 20 // Silero VAD fixed window size
	return &boundaryDetector{
		minSpeechFrames:  max(1, ceilDiv(cfg.MinSpeechDurationMs, chunkMs)),
		minSilenceFrames: max(1, ceilDiv(cfg.MinSilenceDurationMs, chunkMs)),
	}
}

// ceilDiv returns the ceiling of a/b for positive integers.
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func (bd *boundaryDetector) process(result engine.Result) []*napv1.SpeechEvent {
	bd.lastConfidence = result.Confidence
	var events []*napv1.SpeechEvent

	if result.IsSpeech {
		bd.speechFrames++
		bd.silenceFrames = 0

		if !bd.inSpeech && bd.speechFrames >= bd.minSpeechFrames {
			bd.inSpeech = true
			events = append(events, &napv1.SpeechEvent{
				Type:       napv1.SpeechEventType_SPEECH_EVENT_TYPE_START,
				Confidence: result.Confidence,
			})
		} else if bd.inSpeech {
			events = append(events, &napv1.SpeechEvent{
				Type:       napv1.SpeechEventType_SPEECH_EVENT_TYPE_ONGOING,
				Confidence: result.Confidence,
			})
		}
	} else {
		bd.silenceFrames++
		bd.speechFrames = 0

		if bd.inSpeech && bd.silenceFrames >= bd.minSilenceFrames {
			bd.inSpeech = false
			events = append(events, &napv1.SpeechEvent{
				Type:       napv1.SpeechEventType_SPEECH_EVENT_TYPE_END,
				Confidence: result.Confidence,
			})
		}
	}

	return events
}
