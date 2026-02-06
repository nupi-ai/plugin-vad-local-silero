package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	napv1 "github.com/nupi-ai/nupi/api/nap/v1"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
	"github.com/nupi-ai/plugin-vad-local-silero/internal/engine"
	"github.com/nupi-ai/plugin-vad-local-silero/internal/server"
)

// version is set at build time by GoReleaser via -ldflags.
var version = "dev"

// lazyVADServer wraps a VoiceActivityDetectionServiceServer and allows deferred
// initialization. It returns Unavailable errors until the underlying server is set.
type lazyVADServer struct {
	napv1.UnimplementedVoiceActivityDetectionServiceServer
	server atomic.Pointer[napv1.VoiceActivityDetectionServiceServer]
}

func (l *lazyVADServer) setServer(srv napv1.VoiceActivityDetectionServiceServer) {
	l.server.Store(&srv)
}

func (l *lazyVADServer) DetectSpeech(stream napv1.VoiceActivityDetectionService_DetectSpeechServer) error {
	srv := l.server.Load()
	if srv == nil {
		return status.Error(codes.Unavailable, "VAD service is initializing, please retry in a moment")
	}
	return (*srv).DetectSpeech(stream)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	loadResult, err := config.Loader{}.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	cfg := loadResult.Config

	logger := newLogger(cfg.LogLevel)

	// Log warnings for deprecated/unsupported config options.
	for _, warn := range loadResult.Warnings {
		logger.Warn(warn)
	}

	logger.Info("starting adapter",
		"adapter", "vad-local-silero",
		"version", version,
		"engine_config", cfg.Engine, // configured value, may be "auto"
		"listen_addr", cfg.ListenAddr,
		"threshold", cfg.Threshold,
		"min_speech_duration_ms", cfg.MinSpeechDurationMs,
		"min_silence_duration_ms", cfg.MinSilenceDurationMs,
	)

	// STEP 1: Bind port IMMEDIATELY (before engine init)
	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Error("failed to bind listener", "error", err)
		os.Exit(1)
	}
	defer lis.Close()
	logger.Info("listener bound, port ready", "addr", lis.Addr().String())

	// STEP 2: Setup gRPC server with lazy VAD service wrapper
	// Limit message size to prevent memory spikes from oversized payloads.
	// Add 64KB headroom for protobuf overhead beyond PCM data.
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(server.MaxPCMChunkBytes+64*1024),
	)
	healthServer := health.NewServer()
	healthgrpc.RegisterHealthServer(grpcServer, healthServer)

	serviceName := napv1.VoiceActivityDetectionService_ServiceDesc.ServiceName
	healthServer.SetServingStatus("", healthgrpc.HealthCheckResponse_NOT_SERVING)
	healthServer.SetServingStatus(serviceName, healthgrpc.HealthCheckResponse_NOT_SERVING)

	lazyService := &lazyVADServer{}
	napv1.RegisterVoiceActivityDetectionServiceServer(grpcServer, lazyService)

	// STEP 3: Start gRPC server in background
	serverErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serverErr <- err
		}
	}()
	logger.Info("gRPC server started (NOT_SERVING while initializing)")

	// STEP 4: Engine factory — each stream gets its own engine instance.
	// Resolve "auto" to actual engine based on what's compiled in and working.
	resolvedEngine := cfg.Engine
	isAutoMode := resolvedEngine == "auto"

	if isAutoMode {
		if engine.NativeAvailable() {
			resolvedEngine = "silero"
		} else {
			resolvedEngine = "stub"
			logger.Warn("auto-detected engine: stub (native silero not compiled in, build with -tags silero for production)")
		}
	}

	var newEngine func() engine.Engine
	switch resolvedEngine {
	case "silero":
		if !engine.NativeAvailable() {
			logger.Error("engine \"silero\" requested but native backend not compiled in (build with -tags silero)")
			os.Exit(1)
		}
		// Probe: verify native engine can be created before accepting traffic.
		probe, err := engine.NewNativeEngine(cfg.Threshold)
		if err != nil {
			devMode := os.Getenv("NUPI_DEV_MODE") == "1"
			if isAutoMode && devMode {
				// Auto mode + dev mode: fall back to stub instead of failing hard.
				logger.Warn("native engine probe failed, falling back to stub engine (NUPI_DEV_MODE=1)",
					"error", err,
					"hint", "unset NUPI_DEV_MODE for production behavior")
				resolvedEngine = "stub"
				newEngine = func() engine.Engine {
					return engine.NewStubEngine()
				}
			} else {
				// Production or explicit silero: fail hard.
				logger.Error("native engine probe failed — cannot start", "error", err)
				if isAutoMode {
					logger.Error("hint: set NUPI_DEV_MODE=1 to allow fallback to stub engine")
				}
				os.Exit(1)
			}
		} else {
			probe.Close()
			logger.Info("engine ready", "type", "silero")

			// TODO(perf): For high concurrency, consider pooling ONNX sessions or
			// sharing a single session with per-stream RNN state. Currently each
			// stream creates its own session and tensors, which scales linearly.
			newEngine = func() engine.Engine {
				eng, err := engine.NewNativeEngine(cfg.Threshold)
				if err != nil {
					// Should not happen after successful probe; return nil,
					// handled by server as stream error.
					logger.Error("per-stream engine creation failed", "error", err)
					return nil
				}
				return eng
			}
		}
	case "stub":
		logger.Warn("using stub engine — VAD results are deterministic and NOT based on audio content")
		newEngine = func() engine.Engine {
			return engine.NewStubEngine()
		}
	}

	// STEP 5: Activate the real VAD service
	realService := server.New(cfg, logger, newEngine)
	lazyService.setServer(napv1.VoiceActivityDetectionServiceServer(realService))

	healthServer.SetServingStatus("", healthgrpc.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(serviceName, healthgrpc.HealthCheckResponse_SERVING)
	logger.Info("adapter ready to serve requests", "engine", resolvedEngine)

	// STEP 6: Setup graceful shutdown
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		logger.Info("shutdown requested, stopping gRPC server")
		healthServer.SetServingStatus(serviceName, healthgrpc.HealthCheckResponse_NOT_SERVING)
		healthServer.SetServingStatus("", healthgrpc.HealthCheckResponse_NOT_SERVING)

		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			logger.Warn("graceful stop timed out, forcing stop")
			grpcServer.Stop()
		}
		close(shutdownDone)
	}()

	// STEP 7: Wait for server to finish or error
	select {
	case err := <-serverErr:
		logger.Error("gRPC server terminated with error", "error", err)
		os.Exit(1)
	case <-shutdownDone:
		// Normal shutdown — graceful drain completed
	}

	logger.Info("adapter stopped")
}

func newLogger(level string) *slog.Logger {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler)
}

func parseLevel(value string) slog.Leveler {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
