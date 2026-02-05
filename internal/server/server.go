package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	napv1 "github.com/nupi-ai/nupi/api/nap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
	"github.com/nupi-ai/plugin-vad-local-silero/internal/engine"
)

// MaxPCMChunkBytes limits the size of a single PCM chunk to prevent
// memory spikes from oversized messages. 1 MB ≈ 32 seconds at 16 kHz mono s16le.
// This is also enforced at gRPC transport level via MaxRecvMsgSize.
const MaxPCMChunkBytes = 1 << 20

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
	// Engine is created lazily on first PCM to avoid resource waste from idle streams.
	streamCfg := s.cfg
	var eng engine.Engine
	defer func() {
		if eng != nil {
			eng.Close()
		}
	}()

	var (
		engineReady     bool // engine created and configured
		formatKnown     bool // audio format validated (at first PCM)
		bd              *boundaryDetector
		cachedFormat    *napv1.AudioFormat // cached from any message (for clients that send format before PCM)
		sampleRate      uint32
		frameDurationMs int
		streamStart     time.Time
		frameCount      int64
		sessionId       string
		streamId        string
	)

	// initEngine creates the engine and applies config. Called once on first PCM.
	initEngine := func() error {
		if engineReady {
			return nil
		}
		eng = s.newEngine()
		if eng == nil {
			return status.Error(codes.Internal, "engine creation failed: factory returned nil")
		}
		eng.SetThreshold(streamCfg.Threshold)
		frameDurationMs = eng.FrameDurationMs()
		if frameDurationMs <= 0 {
			return status.Errorf(codes.Internal, "engine returned invalid frame duration: %d ms", frameDurationMs)
		}
		bd = newBoundaryDetector(streamCfg, frameDurationMs)
		engineReady = true
		return nil
	}

	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Client closed the stream — flush any pending speech end.
				if bd != nil && bd.inSpeech {
					ts := streamStart.Add(time.Duration(frameCount) * time.Duration(frameDurationMs) * time.Millisecond)
					if sendErr := stream.Send(&napv1.SpeechEvent{
						Type:       napv1.SpeechEventType_SPEECH_EVENT_TYPE_END,
						Confidence: bd.lastConfidence,
						Timestamp:  timestamppb.New(ts),
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

		// Capture session/stream IDs from any request that provides them.
		if sessionId == "" {
			if id := req.GetSessionId(); id != "" {
				sessionId = id
			}
		}
		if streamId == "" {
			if id := req.GetStreamId(); id != "" {
				streamId = id
			}
		}

		// Cache/update audio format from any message until first PCM.
		// Only cache formats with sample_rate > 0 to avoid overwriting valid
		// formats with incomplete ones (e.g., keepalive with empty format {}).
		// Validate all fields at cache time to reject invalid formats early.
		if !formatKnown {
			if af := req.GetFormat(); af != nil && af.GetSampleRate() > 0 {
				// Validate format fields before caching to prevent invalid formats
				// from slipping through when PCM arrives without format.
				if enc := af.GetEncoding(); enc != "" && enc != "pcm_s16le" {
					return status.Errorf(codes.InvalidArgument,
						"unsupported encoding %q, only pcm_s16le is supported", enc)
				}
				if ch := af.GetChannels(); ch != 0 && ch != 1 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported channels %d, only mono (1) is supported", ch)
				}
				if bits := af.GetBitDepth(); bits != 0 && bits != 16 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported bit_depth %d, only 16-bit is supported", bits)
				}
				if af.GetSampleRate() != engine.ExpectedSampleRate {
					return status.Errorf(codes.InvalidArgument,
						"unsupported sample_rate %d, engine requires %d", af.GetSampleRate(), engine.ExpectedSampleRate)
				}
				cachedFormat = af
			}
		} else {
			// After format is established, validate consistency on ALL messages
			// (including empty ones) to catch protocol misuse early.
			if af := req.GetFormat(); af != nil {
				if sr := af.GetSampleRate(); sr != 0 && sr != sampleRate {
					return status.Errorf(codes.InvalidArgument,
						"sample_rate changed mid-stream: initial=%d, got=%d", sampleRate, sr)
				}
				if enc := af.GetEncoding(); enc != "" && enc != "pcm_s16le" {
					return status.Errorf(codes.InvalidArgument,
						"unsupported encoding %q, only pcm_s16le is supported", enc)
				}
				if ch := af.GetChannels(); ch != 0 && ch != 1 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported channels %d, only mono (1) is supported", ch)
				}
				if bits := af.GetBitDepth(); bits != 0 && bits != 16 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported bit_depth %d, only 16-bit is supported", bits)
				}
			}
		}

		// Skip empty chunks (config-only or keepalive messages).
		pcm := req.GetPcmData()
		if len(pcm) == 0 {
			// Apply config_json from non-PCM messages (config-only requests).
			// Config can be updated until the first PCM arrives.
			//
			// NOTE: Invalid config_json intentionally returns an error (fail-fast)
			// rather than logging and ignoring. This helps clients catch config
			// bugs early instead of silently using default values.
			if !engineReady {
				if err := applyStreamConfig(req.GetConfigJson(), &streamCfg); err != nil {
					return status.Errorf(codes.InvalidArgument, "stream config: %v", err)
				}
			} else if cj := req.GetConfigJson(); cj != "" {
				// Config after audio started is ignored — log warning for debugging.
				s.log.Warn("config_json ignored after audio started",
					"session_id", sessionId,
					"stream_id", streamId,
				)
			}
			continue
		}

		// Validate audio format BEFORE initializing engine (on first PCM).
		// This prevents DoS via streams with invalid format — we reject early
		// without allocating expensive ONNX sessions.
		if !formatKnown {
			// Always validate req.Format fields (if present) to catch protocol errors,
			// even when using cached format for sample_rate. This prevents masking
			// invalid fields like encoding="wrong" when sample_rate=0.
			if reqFmt := req.GetFormat(); reqFmt != nil {
				// Validate sample_rate consistency with cache (if both are set).
				// This catches client bugs where format changes between messages.
				if cachedFormat != nil {
					if sr := reqFmt.GetSampleRate(); sr != 0 && sr != cachedFormat.GetSampleRate() {
						return status.Errorf(codes.InvalidArgument,
							"sample_rate mismatch: cached=%d, request=%d",
							cachedFormat.GetSampleRate(), sr)
					}
				}
				if enc := reqFmt.GetEncoding(); enc != "" && enc != "pcm_s16le" {
					return status.Errorf(codes.InvalidArgument,
						"unsupported encoding %q, only pcm_s16le is supported", enc)
				}
				if ch := reqFmt.GetChannels(); ch != 0 && ch != 1 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported channels %d, only mono (1) is supported", ch)
				}
				if bits := reqFmt.GetBitDepth(); bits != 0 && bits != 16 {
					return status.Errorf(codes.InvalidArgument,
						"unsupported bit_depth %d, only 16-bit is supported", bits)
				}
			}

			// Determine sample_rate: prefer cached (from earlier message), else from request.
			af := cachedFormat
			if af == nil {
				af = req.GetFormat()
			}
			if af == nil {
				return status.Errorf(codes.InvalidArgument,
					"audio format required: send format with PCM data or in a prior message")
			}
			sampleRate = af.GetSampleRate()
			if sampleRate == 0 {
				return status.Errorf(codes.InvalidArgument,
					"audio format must include sample_rate")
			}
			// Validate against known constant — engine not yet created.
			if sampleRate != engine.ExpectedSampleRate {
				return status.Errorf(codes.InvalidArgument,
					"unsupported sample_rate %d, engine requires %d", sampleRate, engine.ExpectedSampleRate)
			}
			formatKnown = true
		}

		// Validate PCM input BEFORE engine creation to prevent DoS via
		// requests with valid format but invalid PCM (odd length, too large).
		if len(pcm)%2 != 0 {
			return status.Errorf(codes.InvalidArgument,
				"PCM buffer has odd length %d (s16le requires 2 bytes per sample)", len(pcm))
		}
		if len(pcm) > MaxPCMChunkBytes {
			return status.Errorf(codes.InvalidArgument,
				"PCM chunk too large: %d bytes (max %d)", len(pcm), MaxPCMChunkBytes)
		}

		// First PCM: finalize config and initialize engine.
		// Format and PCM already validated above, so engine creation is safe.
		if !engineReady {
			// Apply config_json from the first PCM message (if present).
			// Invalid config returns error intentionally (see NOTE above).
			if err := applyStreamConfig(req.GetConfigJson(), &streamCfg); err != nil {
				return status.Errorf(codes.InvalidArgument, "stream config: %v", err)
			}
			if err := initEngine(); err != nil {
				return err
			}
			s.log.Info("stream opened",
				"session_id", sessionId,
				"stream_id", streamId,
				"sample_rate", sampleRate,
			)
		} else if cj := req.GetConfigJson(); cj != "" {
			// Config after audio started is ignored — log warning for debugging.
			s.log.Warn("config_json ignored after audio started",
				"session_id", sessionId,
				"stream_id", streamId,
			)
		}

		// Anchor stream clock to the first non-empty PCM chunk.
		if streamStart.IsZero() {
			streamStart = time.Now()
		}

		results, err := eng.ProcessChunk(pcm, sampleRate)
		if err != nil {
			s.log.Error("engine error", "error", err)
			return status.Error(codes.Internal, "audio processing failed")
		}

		for _, result := range results {
			events := bd.process(result)
			for _, evt := range events {
				// Timestamp represents AUDIO TIME (position in stream), not wall-clock.
				// Calculated as: streamStart + (frameIndex * frameDurationMs).
				// This is the time when the audio frame occurred relative to stream start,
				// NOT when the event was sent. Under backpressure or large chunks,
				// timestamps may appear "in the future" relative to event delivery time.
				// Clients should use these timestamps for audio synchronization, not
				// as wall-clock event times.
				ts := streamStart.Add(time.Duration(frameCount) * time.Duration(frameDurationMs) * time.Millisecond)
				evt.Timestamp = timestamppb.New(ts)
				if sendErr := stream.Send(evt); sendErr != nil {
					return sendErr
				}
			}
			frameCount++
		}
	}
}

// applyStreamConfig parses optional JSON config from the first request and
// overrides relevant fields in the per-stream config copy. Returns an error
// if the JSON is malformed or the resulting config fails validation.
func applyStreamConfig(configJSON string, cfg *config.Config) error {
	if strings.TrimSpace(configJSON) == "" {
		return nil
	}
	type streamCfg struct {
		Threshold            *float64 `json:"threshold"`
		MinSpeechDurationMs  *int     `json:"min_speech_duration_ms"`
		MinSilenceDurationMs *int     `json:"min_silence_duration_ms"`
		SpeechPadMs          *int     `json:"speech_pad_ms"` // unsupported, for error only
	}
	var sc streamCfg
	if err := json.Unmarshal([]byte(configJSON), &sc); err != nil {
		return fmt.Errorf("invalid config_json: %w", err)
	}
	if sc.SpeechPadMs != nil {
		return fmt.Errorf("speech_pad_ms is not supported; use min_speech_duration_ms and min_silence_duration_ms instead")
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
	return cfg.ValidateVADParams()
}

// boundaryDetector applies hysteresis to raw per-frame engine results,
// emitting speech events only after sustained speech/silence thresholds.
//
// NOTE: threshold is applied inside the engine (Engine.ProcessChunk returns
// IsSpeech already thresholded). Speech boundary padding (lookahead/lookbehind)
// is not yet implemented and may be added in a future version.
//
// Frame duration is provided by Engine.FrameDurationMs() — 20ms for StubEngine,
// 32ms for SileroEngine (512 samples at 16kHz). Each Result in the slice
// returned by ProcessChunk represents one inferred frame.
type boundaryDetector struct {
	inSpeech       bool
	speechFrames   int
	silenceFrames  int
	lastConfidence float32

	// Derived from config: number of consecutive frames needed.
	minSpeechFrames  int
	minSilenceFrames int
}

func newBoundaryDetector(cfg config.Config, frameDurationMs int) *boundaryDetector {
	return &boundaryDetector{
		minSpeechFrames:  max(1, ceilDiv(cfg.MinSpeechDurationMs, frameDurationMs)),
		minSilenceFrames: max(1, ceilDiv(cfg.MinSilenceDurationMs, frameDurationMs)),
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
