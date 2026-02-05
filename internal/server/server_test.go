package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	napv1 "github.com/nupi-ai/nupi/api/nap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/nupi-ai/plugin-vad-local-silero/internal/config"
	"github.com/nupi-ai/plugin-vad-local-silero/internal/engine"
)

// startTestServer creates a gRPC server with the VAD service using a
// StubEngine factory. It returns a client connection and a cleanup function.
func startTestServer(t *testing.T, cfg config.Config) (napv1.VoiceActivityDetectionServiceClient, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	newEngine := func() engine.Engine { return engine.NewStubEngine() }
	logger := slog.Default()
	srv := New(cfg, logger, newEngine)

	grpcServer := grpc.NewServer()
	napv1.RegisterVoiceActivityDetectionServiceServer(grpcServer, srv)
	go grpcServer.Serve(lis)

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcServer.Stop()
		t.Fatal(err)
	}

	client := napv1.NewVoiceActivityDetectionServiceClient(conn)
	cleanup := func() {
		conn.Close()
		grpcServer.Stop()
	}
	return client, cleanup
}

func TestDetectSpeechSpeechStartEnd(t *testing.T) {
	// Use small durations so the boundary detector triggers quickly.
	// StubEngine.FrameDurationMs()=20: minSpeechFrames = 20/20 = 1, minSilenceFrames = 20/20 = 1.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// StubEngine: counter increments before check, toggles at StubToggleInterval (50).
	// Chunks 1..49 → silence (no events from boundary detector for silence when not in speech).
	// Chunk 50 → toggles to speech → first speech frame → SPEECH_START (minSpeechFrames=1).
	// Chunk 51 → speech → SPEECH_ONGOING.
	// We need to send enough chunks to see START, then close to get END.

	chunk := make([]byte, 640) // 20ms at 16kHz, 16-bit mono
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "test-session",
			StreamId:  "test-stream",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Close the send side to signal EOF.
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	// Collect all events.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	// We expect: SPEECH_START (chunk 50), SPEECH_ONGOING (chunk 51), SPEECH_END (EOF flush).
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2 (START + END)", len(events))
	}

	// First event should be START.
	if events[0].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
		t.Errorf("events[0].Type = %v, want SPEECH_EVENT_TYPE_START", events[0].Type)
	}
	if events[0].Confidence != engine.StubConfidence {
		t.Errorf("events[0].Confidence = %v, want %v", events[0].Confidence, engine.StubConfidence)
	}

	// Last event should be END (flush on EOF while in speech state).
	last := events[len(events)-1]
	if last.Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_END {
		t.Errorf("last event Type = %v, want SPEECH_EVENT_TYPE_END", last.Type)
	}

	// All intermediate events (if any) should be ONGOING.
	for i := 1; i < len(events)-1; i++ {
		if events[i].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_ONGOING {
			t.Errorf("events[%d].Type = %v, want SPEECH_EVENT_TYPE_ONGOING", i, events[i].Type)
		}
	}
}

func TestDetectSpeechSilenceOnly(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send fewer chunks than the toggle interval — all silence.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval-5; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "test-session",
			StreamId:  "test-stream",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	// No speech events expected — silence doesn't generate events.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	if len(events) != 0 {
		t.Errorf("got %d events for silence-only stream, want 0", len(events))
	}
}

func TestDetectSpeechFullCycle(t *testing.T) {
	// Send enough chunks to go through silence → speech → silence again.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send 2 full cycles: silence(49) + speech(50) + silence(50) = 149 chunks.
	// That should produce: START, ONGOING*49, END (from silence transition).
	totalChunks := engine.StubToggleInterval*3 - 1
	chunk := make([]byte, 640)
	for i := 0; i < totalChunks; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "test-session",
			StreamId:  "test-stream",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	var eventTypes []napv1.SpeechEventType
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		eventTypes = append(eventTypes, evt.Type)
	}

	if len(eventTypes) == 0 {
		t.Fatal("expected events but got none")
	}

	// First must be START, last must be END.
	if eventTypes[0] != napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
		t.Errorf("first event = %v, want START", eventTypes[0])
	}
	lastType := eventTypes[len(eventTypes)-1]
	if lastType != napv1.SpeechEventType_SPEECH_EVENT_TYPE_END {
		t.Errorf("last event = %v, want END", lastType)
	}

	// Count event types.
	counts := map[napv1.SpeechEventType]int{}
	for _, et := range eventTypes {
		counts[et]++
	}
	if counts[napv1.SpeechEventType_SPEECH_EVENT_TYPE_START] != 1 {
		t.Errorf("START count = %d, want 1", counts[napv1.SpeechEventType_SPEECH_EVENT_TYPE_START])
	}
	if counts[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END] != 1 {
		t.Errorf("END count = %d, want 1", counts[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END])
	}
}

func TestDetectSpeechConcurrentStreamsIsolation(t *testing.T) {
	// Two concurrent streams must not share engine state. Each gets its own
	// StubEngine via the factory, so their toggle counters are independent.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	const numStreams = 3
	type streamResult struct {
		startCount int
		endCount   int
		err        error
	}
	results := make([]streamResult, numStreams)

	var wg sync.WaitGroup
	wg.Add(numStreams)

	for s := 0; s < numStreams; s++ {
		go func(idx int) {
			defer wg.Done()

			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				results[idx].err = err
				return
			}

			// Each stream sends enough for a full speech cycle independently.
			totalChunks := engine.StubToggleInterval*3 - 1
			chunk := make([]byte, 640)
			for i := 0; i < totalChunks; i++ {
				if err := stream.Send(&napv1.DetectSpeechRequest{
					PcmData:   chunk,
					Format:    &napv1.AudioFormat{SampleRate: 16000},
					SessionId: "test-session",
					StreamId:  "test-stream",
				}); err != nil {
					results[idx].err = err
					return
				}
			}
			stream.CloseSend()

			for {
				evt, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					results[idx].err = err
					return
				}
				switch evt.Type {
				case napv1.SpeechEventType_SPEECH_EVENT_TYPE_START:
					results[idx].startCount++
				case napv1.SpeechEventType_SPEECH_EVENT_TYPE_END:
					results[idx].endCount++
				}
			}
		}(s)
	}

	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("stream %d: error: %v", i, r.err)
			continue
		}
		if r.startCount != 1 {
			t.Errorf("stream %d: START count = %d, want 1", i, r.startCount)
		}
		if r.endCount != 1 {
			t.Errorf("stream %d: END count = %d, want 1", i, r.endCount)
		}
	}
}

func TestDetectSpeechStreamConfigIsolation(t *testing.T) {
	// Per-stream config_json must not leak between streams.
	// Stream A sends config_json with min_silence_duration_ms=1000 (very high).
	// Stream B uses server defaults (min_silence_duration_ms=20).
	// Both send enough chunks for a full cycle.
	// Stream A should NOT get SPEECH_END mid-stream (high silence threshold).
	// Stream B should get SPEECH_END normally.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	// Stream A: high silence threshold — no mid-stream END expected.
	streamA, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	totalChunks := engine.StubToggleInterval*3 - 1
	chunk := make([]byte, 640)

	// First chunk with config_json override.
	if err := streamA.Send(&napv1.DetectSpeechRequest{
		PcmData:    chunk,
		Format:     &napv1.AudioFormat{SampleRate: 16000},
		SessionId:  "session-a",
		StreamId:   "stream-a",
		ConfigJson: `{"min_silence_duration_ms": 5000}`,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < totalChunks; i++ {
		if err := streamA.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "session-a",
			StreamId:  "stream-a",
		}); err != nil {
			t.Fatal(err)
		}
	}
	streamA.CloseSend()

	// Stream B: default config — should see normal END.
	streamB, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < totalChunks; i++ {
		if err := streamB.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "session-b",
			StreamId:  "stream-b",
		}); err != nil {
			t.Fatal(err)
		}
	}
	streamB.CloseSend()

	// Collect events from both streams.
	collectEvents := func(stream napv1.VoiceActivityDetectionService_DetectSpeechClient) map[napv1.SpeechEventType]int {
		counts := map[napv1.SpeechEventType]int{}
		for {
			evt, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			counts[evt.Type]++
		}
		return counts
	}

	countsA := collectEvents(streamA)
	countsB := collectEvents(streamB)

	// Stream A: with min_silence_duration_ms=5000, the 50-chunk silence interval
	// (~1s) is far below the threshold (~250 frames), so no mid-stream SPEECH_END.
	// It should get START + ONGOING + EOF-flush END.
	if countsA[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END] != 1 {
		t.Errorf("stream A: END count = %d, want 1 (EOF flush only)", countsA[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END])
	}

	// Stream B: default thresholds, should get normal START + END cycle.
	if countsB[napv1.SpeechEventType_SPEECH_EVENT_TYPE_START] != 1 {
		t.Errorf("stream B: START count = %d, want 1", countsB[napv1.SpeechEventType_SPEECH_EVENT_TYPE_START])
	}
	if countsB[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END] != 1 {
		t.Errorf("stream B: END count = %d, want 1", countsB[napv1.SpeechEventType_SPEECH_EVENT_TYPE_END])
	}
}

func TestCeilDiv(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{250, 20, 13},  // 250ms / 20ms = 12.5 → ceil = 13
		{300, 20, 15},  // exact division
		{20, 20, 1},    // single frame
		{1, 20, 1},     // sub-frame rounds up
		{0, 20, 0},     // zero
		{19, 20, 1},    // just under one frame
		{21, 20, 2},    // just over one frame
	}
	for _, tt := range tests {
		got := ceilDiv(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("ceilDiv(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDetectSpeechInvalidStreamConfig(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	tests := []struct {
		name       string
		configJSON string
	}{
		{"threshold_negative", `{"threshold": -0.5}`},
		{"threshold_above_one", `{"threshold": 1.5}`},
		{"min_speech_zero", `{"min_speech_duration_ms": 0}`},
		{"min_silence_negative", `{"min_silence_duration_ms": -1}`},
		{"invalid_json", `{bad json}`},
		{"speech_pad_ms_unsupported", `{"speech_pad_ms": 100}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			chunk := make([]byte, 640)
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData:    chunk,
				Format:     &napv1.AudioFormat{SampleRate: 16000},
				SessionId:  "test-session",
				StreamId:   "test-stream",
				ConfigJson: tt.configJSON,
			}); err != nil {
				t.Fatal(err)
			}

			// The server should reject invalid config_json with an error.
			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid stream config, got nil")
			}
		})
	}
}

func TestDetectSpeechInvalidStreamConfigPrePCM(t *testing.T) {
	// Test that invalid config_json in a config-only (pre-PCM) message
	// returns InvalidArgument. This is the stricter behavior for early
	// detection of client configuration errors.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	tests := []struct {
		name       string
		configJSON string
		wantMsg    string
	}{
		{"invalid_json_prePCM", `{bad json}`, "config_json"},
		{"threshold_negative_prePCM", `{"threshold": -0.5}`, "threshold"},
		{"min_speech_zero_prePCM", `{"min_speech_duration_ms": 0}`, "min_speech"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// Send config-only message (no PCM) with invalid config_json.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				Format:     &napv1.AudioFormat{SampleRate: 16000},
				SessionId:  "prePCM-config-test",
				StreamId:   "prePCM-config-test",
				ConfigJson: tt.configJSON,
				// No PcmData — this is a config-only message
			}); err != nil {
				t.Fatal(err)
			}

			// Send a second message to trigger server response.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData: make([]byte, 640),
				Format:  &napv1.AudioFormat{SampleRate: 16000},
			}); err != nil {
				// May fail if server already closed — that's fine
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid config_json in pre-PCM message")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}
		})
	}
}

func TestDetectSpeechSubThresholdSpeechDiscarded(t *testing.T) {
	// Speech frames that don't reach minSpeechFrames before silence returns
	// must NOT emit SPEECH_START. This tests the hysteresis correctly discards
	// brief speech bursts.
	//
	// StubEngine.FrameDurationMs()=20.
	// With minSpeechDurationMs=1100 → minSpeechFrames = ceilDiv(1100,20) = 55.
	// StubEngine's speech interval is only 50 frames → never reaches threshold.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  1100, // 55 frames needed, stub only produces 50
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send 3 full toggle intervals: silence(49) + speech(50) + silence(50) = 149.
	// Speech burst is 50 frames but threshold is 55 → no START emitted.
	totalChunks := engine.StubToggleInterval*3 - 1
	chunk := make([]byte, 640)
	for i := 0; i < totalChunks; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData:   chunk,
			Format:    &napv1.AudioFormat{SampleRate: 16000},
			SessionId: "test-session",
			StreamId:  "test-stream",
		}); err != nil {
			t.Fatal(err)
		}
	}
	stream.CloseSend()

	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	// No events expected: speech burst too short for START, never entered
	// speech state so no END either.
	if len(events) != 0 {
		types := make([]string, len(events))
		for i, e := range events {
			types[i] = e.Type.String()
		}
		t.Errorf("expected 0 events for sub-threshold speech, got %d: %v", len(events), types)
	}
}

func TestDetectSpeechConfigOnlyBeforeAudio(t *testing.T) {
	// Config-only messages before audio should be allowed.
	// Format is only required with the first PCM data.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send config-only request (no PCM, no format) — should be accepted.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		SessionId:  "config-test",
		StreamId:   "config-test",
		ConfigJson: `{"threshold": 0.7}`,
	}); err != nil {
		t.Fatal(err)
	}

	// Send keepalive (empty request) — should be accepted.
	if err := stream.Send(&napv1.DetectSpeechRequest{}); err != nil {
		t.Fatal(err)
	}

	// Now send audio with format — should work.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
			Format:  &napv1.AudioFormat{SampleRate: 16000},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stream.CloseSend()

	// Should get events normally.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, evt)
	}

	if len(events) < 2 {
		t.Errorf("expected at least 2 events (START + END), got %d", len(events))
	}
}

func TestDetectSpeechConfigAfterKeepalive(t *testing.T) {
	// config_json should be accepted even after empty keepalive messages,
	// as long as no PCM has been sent yet.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  1100, // 55 frames — stub only produces 50 speech frames
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send empty keepalives first.
	for i := 0; i < 3; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{}); err != nil {
			t.Fatal(err)
		}
	}

	// Now send config_json lowering min_speech_duration_ms to 20 (1 frame).
	// If config is still open, this should take effect.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		SessionId:  "config-after-keepalive",
		StreamId:   "config-after-keepalive",
		ConfigJson: `{"min_speech_duration_ms": 20}`,
	}); err != nil {
		t.Fatal(err)
	}

	// Send audio — with lowered threshold, we should get SPEECH_START.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
			Format:  &napv1.AudioFormat{SampleRate: 16000},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stream.CloseSend()

	var gotStart bool
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.Type == napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
			gotStart = true
		}
	}

	if !gotStart {
		t.Error("expected SPEECH_START — config_json after keepalive should have been applied")
	}
}

func TestDetectSpeechInvalidAudioFormat(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	tests := []struct {
		name    string
		format  *napv1.AudioFormat
		wantMsg string
	}{
		{"missing_format", nil, "audio format required"},
		{"zero_sample_rate", &napv1.AudioFormat{}, "sample_rate"},
		{"wrong_sample_rate", &napv1.AudioFormat{SampleRate: 44100}, "sample_rate"},
		{"wrong_encoding", &napv1.AudioFormat{SampleRate: 16000, Encoding: "pcm_f32le"}, "encoding"},
		{"stereo_channels", &napv1.AudioFormat{SampleRate: 16000, Channels: 2}, "channels"},
		{"wrong_bit_depth", &napv1.AudioFormat{SampleRate: 16000, BitDepth: 24}, "bit_depth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData:   make([]byte, 640),
				Format:    tt.format,
				SessionId: "test-session",
				StreamId:  "test-stream",
			}); err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid audio format")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}
			if !strings.Contains(st.Message(), tt.wantMsg) {
				t.Errorf("error should mention %q, got: %v", tt.wantMsg, st.Message())
			}
		})
	}
}

func TestDetectSpeechSampleRateChangeMidStream(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	chunk := make([]byte, 640)
	// First request — valid 16kHz.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData:   chunk,
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "test-session",
		StreamId:  "test-stream",
	}); err != nil {
		t.Fatal(err)
	}
	// Second request — different sample rate.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData: chunk,
		Format:  &napv1.AudioFormat{SampleRate: 44100},
	}); err != nil {
		t.Fatal(err)
	}
	stream.CloseSend()

	// Drain responses until error.
	var recvErr error
	for {
		_, recvErr = stream.Recv()
		if recvErr != nil {
			break
		}
	}
	if recvErr == io.EOF {
		t.Fatal("expected error for sample_rate change, got clean EOF")
	}
	st, ok := status.FromError(recvErr)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), recvErr)
	}
}

func TestDetectSpeechOddPCMLength(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send odd-length PCM data (641 bytes — not valid s16le).
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData:   make([]byte, 641),
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "test-session",
		StreamId:  "test-stream",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for odd PCM length")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
	}
}

func TestDetectSpeechPCMChunkTooLarge(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send a chunk exceeding MaxPCMChunkBytes (1 MB).
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData:   make([]byte, MaxPCMChunkBytes+2),
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "test-session",
		StreamId:  "test-stream",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for oversized PCM chunk")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
	}
	if !strings.Contains(st.Message(), "too large") {
		t.Errorf("error should mention 'too large', got: %v", st.Message())
	}
}

func TestDetectSpeechFormatChangeMidStream(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	tests := []struct {
		name    string
		format  *napv1.AudioFormat
		wantMsg string
	}{
		{"encoding_change", &napv1.AudioFormat{SampleRate: 16000, Encoding: "pcm_f32le"}, "encoding"},
		{"channels_change", &napv1.AudioFormat{SampleRate: 16000, Channels: 2}, "channels"},
		{"bit_depth_change", &napv1.AudioFormat{SampleRate: 16000, BitDepth: 24}, "bit_depth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			chunk := make([]byte, 640)
			// First request — valid format.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData:   chunk,
				Format:    &napv1.AudioFormat{SampleRate: 16000},
				SessionId: "test-session",
				StreamId:  "test-stream",
			}); err != nil {
				t.Fatal(err)
			}
			// Second request — changed format field.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData: chunk,
				Format:  tt.format,
			}); err != nil {
				t.Fatal(err)
			}
			stream.CloseSend()

			// Drain responses until error.
			var recvErr error
			for {
				_, recvErr = stream.Recv()
				if recvErr != nil {
					break
				}
			}
			if recvErr == io.EOF {
				t.Fatal("expected error for format change, got clean EOF")
			}
			st, ok := status.FromError(recvErr)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), recvErr)
			}
			if !strings.Contains(st.Message(), tt.wantMsg) {
				t.Errorf("error should mention %q, got: %v", tt.wantMsg, st.Message())
			}
		})
	}
}

func TestDetectSpeechTimestampAnchoredToFirstPCM(t *testing.T) {
	// Timestamps must be anchored to the first non-empty PCM chunk, not the
	// first request. Sending config-only/keepalive messages before audio should
	// not shift timestamps earlier.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send format-only request (no PCM) — triggers init but no audio processing.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "ts-test",
		StreamId:  "ts-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Send keepalive messages to simulate idle time before audio.
	for i := 0; i < 3; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{}); err != nil {
			t.Fatal(err)
		}
	}

	// Sleep to create a measurable gap between the first request and the
	// first audio. Without the fix, streamStart would be ~200ms too early.
	time.Sleep(200 * time.Millisecond)
	beforeAudio := time.Now()

	// Send enough audio to trigger SPEECH_START.
	chunk := make([]byte, 640) // 20ms at 16kHz mono s16le
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
			Format:  &napv1.AudioFormat{SampleRate: 16000},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stream.CloseSend()

	// Collect events.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	// The first event (SPEECH_START) occurs at frame index (StubToggleInterval - 1).
	// Its timestamp is: streamStart + (StubToggleInterval - 1) * frameDurationMs.
	// We can infer streamStart from the observed timestamp and verify it was
	// anchored to the first PCM chunk (>= beforeAudio), not the first request.
	const stubFrameDurationMs = 20
	firstTS := events[0].Timestamp.AsTime()
	frameOffset := time.Duration(engine.StubToggleInterval-1) * stubFrameDurationMs * time.Millisecond
	inferredStreamStart := firstTS.Add(-frameOffset)

	// Allow 50ms tolerance for clock jitter between goroutines.
	tolerance := 50 * time.Millisecond
	if inferredStreamStart.Before(beforeAudio.Add(-tolerance)) {
		t.Errorf("inferred streamStart %v is before audio start %v (gap=%v); "+
			"timestamps should be anchored to first PCM data, not first request",
			inferredStreamStart, beforeAudio,
			beforeAudio.Sub(inferredStreamStart))
	}
}

func TestDetectSpeechEmptyPCMSkipped(t *testing.T) {
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First request: format + config only, no audio.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "test-session",
		StreamId:  "test-stream",
	}); err != nil {
		t.Fatal(err)
	}

	// Send a few empty keepalive messages — should not advance engine state.
	for i := 0; i < 5; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{}); err != nil {
			t.Fatal(err)
		}
	}

	// Now send real silence-only audio (less than toggle interval).
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval-10; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
			Format:  &napv1.AudioFormat{SampleRate: 16000},
		}); err != nil {
			t.Fatal(err)
		}
	}
	stream.CloseSend()

	// No speech events expected — the empty chunks should not have
	// contributed to the engine's toggle counter.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	if len(events) != 0 {
		t.Errorf("got %d events, want 0 (empty chunks should not advance engine)", len(events))
	}
}

func TestDetectSpeechFormatCachedFromEarlierMessage(t *testing.T) {
	// Format sent in an early message (before PCM) should be cached and used
	// when PCM arrives without format.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First message: format only (no PCM) — this should be cached.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "format-cache-test",
		StreamId:  "format-cache-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Second message: PCM only (no format) — should use cached format.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
			// No Format field — relies on cached format
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	// Stream should work — collect events (should see SPEECH_START at minimum).
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error (format should have been cached): %v", err)
		}
		events = append(events, evt)
	}

	// Expect START and END events (same as normal flow).
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2 (START + END)", len(events))
	}
	if events[0].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
		t.Errorf("first event = %v, want SPEECH_EVENT_TYPE_START", events[0].Type)
	}
}

func TestDetectSpeechFormatUpdatedBeforePCM(t *testing.T) {
	// Format can be updated multiple times before first PCM. The last valid
	// format should be used. This supports clients that send placeholder/incomplete
	// formats and then update them.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First message: incomplete format (sample_rate=0).
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 0}, // incomplete
		SessionId: "format-update-test",
		StreamId:  "format-update-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Second message: correct format (sample_rate=16000).
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format: &napv1.AudioFormat{SampleRate: 16000}, // valid
	}); err != nil {
		t.Fatal(err)
	}

	// Third message: PCM without format — should use the updated format.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	// Stream should work — collect events.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error (format should have been updated): %v", err)
		}
		events = append(events, evt)
	}

	// Expect START and END events (same as normal flow).
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2 (START + END)", len(events))
	}
	if events[0].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
		t.Errorf("first event = %v, want SPEECH_EVENT_TYPE_START", events[0].Type)
	}
}

func TestDetectSpeechEngineNilReturnsGRPCError(t *testing.T) {
	// When engine factory returns nil, server should return a proper gRPC
	// status error (not plain error) so clients can react appropriately.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	// Factory that always returns nil.
	newEngine := func() engine.Engine { return nil }
	logger := slog.Default()
	srv := New(cfg, logger, newEngine)

	grpcServer := grpc.NewServer()
	napv1.RegisterVoiceActivityDetectionServiceServer(grpcServer, srv)
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := napv1.NewVoiceActivityDetectionServiceClient(conn)
	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Send a valid request.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData:   make([]byte, 640),
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "nil-engine-test",
		StreamId:  "nil-engine-test",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error when engine is nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("expected Internal code, got: %v", st.Code())
	}
	if !strings.Contains(st.Message(), "engine creation failed") {
		t.Errorf("error should mention 'engine creation failed', got: %v", st.Message())
	}
}

func TestDetectSpeechFormatOnlyAfterPCMValidated(t *testing.T) {
	// Format-only messages (empty PCM) after the first PCM should still be
	// validated for consistency. This catches protocol misuse where a client
	// tries to change format mid-stream via an empty message.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First: send valid PCM with format.
	chunk := make([]byte, 640)
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData:   chunk,
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "format-after-pcm-test",
		StreamId:  "format-after-pcm-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Second: send format-only message (no PCM) with different sample_rate.
	// This should be rejected even though there's no PCM data.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format: &napv1.AudioFormat{SampleRate: 44100}, // different!
	}); err != nil {
		t.Fatal(err)
	}

	stream.CloseSend()

	// Drain responses until error.
	var recvErr error
	for {
		_, recvErr = stream.Recv()
		if recvErr != nil {
			break
		}
	}

	if recvErr == io.EOF {
		t.Fatal("expected error for format change in empty message, got clean EOF")
	}
	st, ok := status.FromError(recvErr)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), recvErr)
	}
	if !strings.Contains(st.Message(), "sample_rate") {
		t.Errorf("error should mention 'sample_rate', got: %v", st.Message())
	}
}

func TestDetectSpeechEmptyFormatDoesNotOverwriteCache(t *testing.T) {
	// Empty format (sample_rate=0) should not overwrite a valid cached format.
	// Scenario: valid format → keepalive with {} → PCM without format → should work.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First: send valid format.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "empty-format-test",
		StreamId:  "empty-format-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Second: send keepalive with empty format (sample_rate=0).
	// This should NOT overwrite the cached valid format.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format: &napv1.AudioFormat{}, // sample_rate=0
	}); err != nil {
		t.Fatal(err)
	}

	// Third: send PCM without format — should use the cached valid format.
	chunk := make([]byte, 640)
	for i := 0; i < engine.StubToggleInterval+1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: chunk,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}

	// Stream should work — collect events.
	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error (empty format should not overwrite cache): %v", err)
		}
		events = append(events, evt)
	}

	// Expect START and END events (same as normal flow).
	if len(events) < 2 {
		t.Fatalf("got %d events, want at least 2 (START + END)", len(events))
	}
}

// startTestServerWithCounter creates a server with an engine factory that counts
// how many times it was called. Used to verify engine is NOT created for invalid requests.
func startTestServerWithCounter(t *testing.T, cfg config.Config) (napv1.VoiceActivityDetectionServiceClient, *int, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	var engineCreateCount int
	newEngine := func() engine.Engine {
		engineCreateCount++
		return engine.NewStubEngine()
	}
	logger := slog.Default()
	srv := New(cfg, logger, newEngine)

	grpcServer := grpc.NewServer()
	napv1.RegisterVoiceActivityDetectionServiceServer(grpcServer, srv)
	go grpcServer.Serve(lis)

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcServer.Stop()
		t.Fatal(err)
	}

	client := napv1.NewVoiceActivityDetectionServiceClient(conn)
	cleanup := func() {
		conn.Close()
		grpcServer.Stop()
	}
	return client, &engineCreateCount, cleanup
}

func TestDetectSpeechEngineNotCreatedForInvalidFormat(t *testing.T) {
	// Verify that for invalid audio formats, the expensive engine (ONNX session)
	// is NOT created. This prevents DoS via streams with invalid formats.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}

	tests := []struct {
		name   string
		format *napv1.AudioFormat
	}{
		{"missing_format", nil},
		{"zero_sample_rate", &napv1.AudioFormat{}},
		{"wrong_sample_rate", &napv1.AudioFormat{SampleRate: 44100}},
		{"wrong_encoding", &napv1.AudioFormat{SampleRate: 16000, Encoding: "pcm_f32le"}},
		{"stereo_channels", &napv1.AudioFormat{SampleRate: 16000, Channels: 2}},
		{"wrong_bit_depth", &napv1.AudioFormat{SampleRate: 16000, BitDepth: 24}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, engineCount, cleanup := startTestServerWithCounter(t, cfg)
			defer cleanup()

			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData:   make([]byte, 640),
				Format:    tt.format,
				SessionId: "test-session",
				StreamId:  "test-stream",
			}); err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid audio format")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}

			// Key assertion: engine should NOT have been created.
			if *engineCount != 0 {
				t.Errorf("engine was created %d times, expected 0 (format validation should happen before engine creation)", *engineCount)
			}
		})
	}
}

func TestDetectSpeechEngineNotCreatedForInvalidPCM(t *testing.T) {
	// Verify that for invalid PCM data (odd length, too large), the expensive
	// engine (ONNX session) is NOT created. This prevents DoS via requests
	// with valid format but invalid PCM payload.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}

	tests := []struct {
		name    string
		pcmData []byte
		wantMsg string
	}{
		{"odd_length", make([]byte, 641), "odd length"},
		{"too_large", make([]byte, MaxPCMChunkBytes+2), "too large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, engineCount, cleanup := startTestServerWithCounter(t, cfg)
			defer cleanup()

			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData:   tt.pcmData,
				Format:    &napv1.AudioFormat{SampleRate: 16000},
				SessionId: "test-session",
				StreamId:  "test-stream",
			}); err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid PCM")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}
			if !strings.Contains(st.Message(), tt.wantMsg) {
				t.Errorf("error should mention %q, got: %v", tt.wantMsg, st.Message())
			}

			// Key assertion: engine should NOT have been created.
			if *engineCount != 0 {
				t.Errorf("engine was created %d times, expected 0 (PCM validation should happen before engine creation)", *engineCount)
			}
		})
	}
}

func TestDetectSpeechInvalidFormatWithCachedSampleRate(t *testing.T) {
	// Verify that invalid format fields (encoding, channels, bit_depth) are
	// validated even when sample_rate comes from cache. This prevents masking
	// protocol errors where client sends Format{SampleRate: 0, Encoding: "wrong"}
	// with PCM and expects the cached sample_rate to be used.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}

	tests := []struct {
		name    string
		format  *napv1.AudioFormat
		wantMsg string
	}{
		{"wrong_encoding_with_zero_sr", &napv1.AudioFormat{SampleRate: 0, Encoding: "pcm_f32le"}, "encoding"},
		{"stereo_with_zero_sr", &napv1.AudioFormat{SampleRate: 0, Channels: 2}, "channels"},
		{"wrong_bit_depth_with_zero_sr", &napv1.AudioFormat{SampleRate: 0, BitDepth: 24}, "bit_depth"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := startTestServer(t, cfg)
			defer cleanup()

			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// First message: cache valid format (16kHz).
			if err := stream.Send(&napv1.DetectSpeechRequest{
				Format:    &napv1.AudioFormat{SampleRate: 16000},
				SessionId: "cache-test",
				StreamId:  "cache-test",
			}); err != nil {
				t.Fatal(err)
			}

			// Second message: PCM with invalid format (sample_rate=0 but bad encoding/channels/etc).
			// Even though sample_rate comes from cache, the invalid fields should be rejected.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData: make([]byte, 640),
				Format:  tt.format,
			}); err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid format field with cached sample_rate")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}
			if !strings.Contains(st.Message(), tt.wantMsg) {
				t.Errorf("error should mention %q, got: %v", tt.wantMsg, st.Message())
			}
		})
	}
}

func TestDetectSpeechInvalidFormatCachedThenPCMWithoutFormat(t *testing.T) {
	// Verify that invalid format fields are rejected at cache time, not when
	// PCM arrives. This prevents invalid formats from slipping through when
	// client sends "format-only invalid" → "PCM without format".
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}

	tests := []struct {
		name    string
		format  *napv1.AudioFormat
		wantMsg string
	}{
		{"wrong_encoding", &napv1.AudioFormat{SampleRate: 16000, Encoding: "pcm_f32le"}, "encoding"},
		{"stereo", &napv1.AudioFormat{SampleRate: 16000, Channels: 2}, "channels"},
		{"wrong_bit_depth", &napv1.AudioFormat{SampleRate: 16000, BitDepth: 24}, "bit_depth"},
		{"wrong_sample_rate", &napv1.AudioFormat{SampleRate: 44100}, "sample_rate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := startTestServer(t, cfg)
			defer cleanup()

			stream, err := client.DetectSpeech(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// First message: format-only with invalid fields.
			// This should be rejected immediately at cache time.
			if err := stream.Send(&napv1.DetectSpeechRequest{
				Format:    tt.format,
				SessionId: "invalid-cache-test",
				StreamId:  "invalid-cache-test",
			}); err != nil {
				t.Fatal(err)
			}

			// Send PCM without format to trigger server response.
			// The error should come from the first message (invalid format at cache time).
			if err := stream.Send(&napv1.DetectSpeechRequest{
				PcmData: make([]byte, 640),
				// No format — would use cache if it were valid
			}); err != nil {
				// Send might fail if server already closed — this is fine
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("expected error for invalid format at cache time")
			}
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
			}
			if !strings.Contains(st.Message(), tt.wantMsg) {
				t.Errorf("error should mention %q, got: %v", tt.wantMsg, st.Message())
			}
		})
	}
}

func TestDetectSpeechSampleRateMismatchWithCache(t *testing.T) {
	// Verify that when format is cached with one sample_rate, sending PCM with
	// a different sample_rate in Format is rejected. This catches client bugs
	// where format changes between messages.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// First message: cache valid format (16kHz).
	if err := stream.Send(&napv1.DetectSpeechRequest{
		Format:    &napv1.AudioFormat{SampleRate: 16000},
		SessionId: "mismatch-test",
		StreamId:  "mismatch-test",
	}); err != nil {
		t.Fatal(err)
	}

	// Second message: PCM with different sample_rate (44100).
	// This should be rejected because it conflicts with the cached format.
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData: make([]byte, 640),
		Format:  &napv1.AudioFormat{SampleRate: 44100},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for sample_rate mismatch with cached format")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got code=%v err=%v", st.Code(), err)
	}
	if !strings.Contains(st.Message(), "sample_rate") && !strings.Contains(st.Message(), "mismatch") {
		t.Errorf("error should mention sample_rate mismatch, got: %v", st.Message())
	}
}

func TestDetectSpeechLargeChunkMultipleEvents(t *testing.T) {
	// Verify that a single large PCM chunk producing multiple engine results
	// generates multiple events with monotonically increasing timestamps.
	// StubEngine: 640 bytes = 1 frame (20ms). 4 frames = 2560 bytes.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
	}
	client, cleanup := startTestServer(t, cfg)
	defer cleanup()

	stream, err := client.DetectSpeech(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	singleFrame := make([]byte, 640) // 320 samples = 20ms

	// Send 49 single-frame chunks (silence period, no events emitted).
	for i := 0; i < engine.StubToggleInterval-1; i++ {
		if err := stream.Send(&napv1.DetectSpeechRequest{
			PcmData: singleFrame,
			Format:  &napv1.AudioFormat{SampleRate: 16000},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Send ONE large chunk containing 4 frames (2560 bytes).
	// Frame 50 toggles to speech → START, frames 51-53 → ONGOING.
	// All 4 events come from a single ProcessChunk call returning 4 Results.
	largeChunk := make([]byte, 640*4)
	if err := stream.Send(&napv1.DetectSpeechRequest{
		PcmData: largeChunk,
		Format:  &napv1.AudioFormat{SampleRate: 16000},
	}); err != nil {
		t.Fatal(err)
	}

	stream.CloseSend()

	var events []*napv1.SpeechEvent
	for {
		evt, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}

	// Expect: START + 3 ONGOING (from big chunk) + END (EOF flush) = 5.
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5 (START + 3 ONGOING + END)", len(events))
	}

	if events[0].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_START {
		t.Errorf("events[0] = %v, want START", events[0].Type)
	}
	for i := 1; i <= 3; i++ {
		if events[i].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_ONGOING {
			t.Errorf("events[%d] = %v, want ONGOING", i, events[i].Type)
		}
	}
	if events[4].Type != napv1.SpeechEventType_SPEECH_EVENT_TYPE_END {
		t.Errorf("events[4] = %v, want END", events[4].Type)
	}

	// Verify timestamps are strictly monotonically increasing.
	for i := 1; i < len(events); i++ {
		prev := events[i-1].Timestamp.AsTime()
		curr := events[i].Timestamp.AsTime()
		if !curr.After(prev) {
			t.Errorf("timestamp[%d] (%v) not after timestamp[%d] (%v)",
				i, curr, i-1, prev)
		}
	}

	// Verify adjacent timestamps from the big chunk differ by exactly 20ms.
	// Events 0..3 come from the same ProcessChunk call (frames 49-52).
	const frameDuration = 20 * time.Millisecond
	for i := 1; i < 4; i++ {
		prev := events[i-1].Timestamp.AsTime()
		curr := events[i].Timestamp.AsTime()
		diff := curr.Sub(prev)
		if diff != frameDuration {
			t.Errorf("timestamp gap events[%d]-[%d] = %v, want %v",
				i, i-1, diff, frameDuration)
		}
	}
}
