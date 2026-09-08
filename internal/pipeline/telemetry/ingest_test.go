package telemetry

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"

	"github.com/nats-io/nats-server/v2/server"
)

// startTestNATSTS 启动一个嵌入式 NATS server（含 JetStream）并返回地址。
func startTestNATSTS(t *testing.T) string {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		NoLog:     true,
		NoSigs:    true,
		StoreDir:  t.TempDir(),
	}
	s, err := server.NewServer(opts)
	require.NoError(t, err)
	go s.Start()
	t.Cleanup(func() { s.Shutdown() })
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server 未就绪")
	}
	tcp, ok := s.Addr().(*net.TCPAddr)
	require.True(t, ok)
	return fmt.Sprintf("nats://%s:%d", opts.Host, tcp.Port)
}

func testFrames(prefix string, n int) []plugin.TelemetryFrame {
	frames := make([]plugin.TelemetryFrame, 0, n)
	for i := 0; i < n; i++ {
		frames = append(frames, plugin.TelemetryFrame{
			SatelliteID: fmt.Sprintf("%s-%d", prefix, i),
			Timestamp:   time.Now(),
			Parameters:  map[string]any{"voltage": 3.3},
			Quality:     "good",
		})
	}
	return frames
}

// TestPipeline_IngestFrames_Batch 验证批量入口 IngestFrames 逐帧入总线且订阅方能收到（T5.4/5.7 batch）。
func TestPipeline_IngestFrames_Batch(t *testing.T) {
	p, _, bus := newTestPipeline(t)

	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	ctx := context.Background()
	accepted, err := p.IngestFrames(ctx, testFrames("batch", 5))
	require.NoError(t, err)
	assert.Equal(t, 5, accepted)

	// 订阅方应陆续收到全部事件
	got := make(map[string]bool)
	deadline := time.After(3 * time.Second)
	for len(got) < 5 {
		select {
		case e := <-ch:
			assert.Equal(t, event.EventTelemetryReceived, e.EventType)
			got[e.SourceNodeID] = true
		case <-deadline:
			t.Fatalf("批量入口事件丢失: 收到 %d/5", len(got))
		}
	}
	assert.Len(t, got, 5)
}

// TestPipeline_IngestFrames_Empty 验证空帧返回错误。
func TestPipeline_IngestFrames_Empty(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	_, err := p.IngestFrames(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未提供")
}

// TestPipeline_IngestFrames_NATS 验证批量入口发布的事件落入 NATS JetStream，
// 且可通过 Replay 读回（T5.7：HTTP/gRPC/batch 入口 → 入 JetStream）。
func TestPipeline_IngestFrames_NATS(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATSTS(t)
	registry := event.NewSchemaRegistry()
	nb, err := core.NewNATSBus(url, registry, nil, core.WithStreamName("events-ingest"))
	require.NoError(t, err)
	defer func() { _ = nb.Close() }()

	broker := plugin.NewBroker(nb, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	p := NewPipeline(mgr, nb, nil)

	ctx := context.Background()
	accepted, err := p.IngestFrames(ctx, testFrames("nats", 10))
	require.NoError(t, err)
	assert.Equal(t, 10, accepted)

	// 等待 async 发布完成
	time.Sleep(300 * time.Millisecond)

	replayed, err := nb.Replay(ctx, core.ReplayOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 10)
	for _, e := range replayed {
		assert.Equal(t, event.EventTelemetryReceived, e.EventType)
	}
}