package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"

	napv1 "github.com/nupi-ai/nupi/api/nap/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
	// With chunkMs=20: minSpeechFrames = 20/20 = 1, minSilenceFrames = 20/20 = 1.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  20,
		MinSilenceDurationMs: 20,
		SpeechPadMs:          0,
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
		SpeechPadMs:          0,
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
		SpeechPadMs:          0,
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
		SpeechPadMs:          0,
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
		SpeechPadMs:          0,
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

func TestDetectSpeechSubThresholdSpeechDiscarded(t *testing.T) {
	// Speech frames that don't reach minSpeechFrames before silence returns
	// must NOT emit SPEECH_START. This tests the hysteresis correctly discards
	// brief speech bursts.
	//
	// Setup: minSpeechFrames = ceilDiv(500, 20) = 25.
	// StubEngine toggles at 50 chunks, so speech lasts 50 frames (enough).
	// But if we set minSpeechDurationMs high enough that it exceeds a toggle
	// interval, the stub's speech burst won't be long enough.
	//
	// With minSpeechDurationMs=1100 → minSpeechFrames = ceilDiv(1100,20) = 55.
	// StubEngine's speech interval is only 50 frames → never reaches threshold.
	cfg := config.Config{
		Threshold:            0.5,
		MinSpeechDurationMs:  1100, // 55 frames needed, stub only produces 50
		MinSilenceDurationMs: 20,
		SpeechPadMs:          0,
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
