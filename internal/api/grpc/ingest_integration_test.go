package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// ingestTestServer 创建带遥测上报流水线的 gRPC 服务器（T5.7 gRPC 入口）。
func ingestTestServer(t *testing.T) (KGServiceClient, core.MessageBus, func()) {
	t.Helper()

	db, err := core.InitDB(":memory:")
	require.NoError(t, err)

	nodeRepo := core.NewSQLiteNodeRepository(db)
	relRepo := core.NewSQLiteRelationshipRepository(db)
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)

	kg := core.NewKGService(nodeRepo, relRepo, bus, nil)
	kg.SetDB(db)

	// 遥测流水线注入 gRPC kgServer
	broker := plugin.NewBroker(bus, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	p := telemetry.NewPipeline(mgr, bus, nil)

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	srv := grpclib.NewServer()
	kgs := newKGServer(kg, bus, registry, nil)
	kgs.setTelemetryIngress(p, nil, nil)
	RegisterKGServiceServer(srv, kgs)
	go func() {
		_ = srv.Serve(lis)
	}()

	conn, err := grpclib.DialContext(context.Background(), "bufnet",
		grpclib.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpclib.WithTransportCredentials(insecure.NewCredentials()),
		grpclib.WithDefaultCallOptions(grpclib.CallContentSubtype("aos")),
	)
	require.NoError(t, err)

	client := NewKGServiceClient(conn)
	cleanup := func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = p.Stop()
		_ = bus.Close()
		_ = db.Close()
	}
	return client, bus, cleanup
}

// TestIngestTelemetry_GRPC 验证 gRPC 客户端流遥测上报能发布事件给订阅方（T5.7 gRPC 入口）。
//
// 客户端流式发送多帧，服务端批量入总线；订阅方应收到全部 TelemetryReceived 事件。
func TestIngestTelemetry_GRPC(t *testing.T) {
	client, bus, cleanup := ingestTestServer(t)
	defer cleanup()

	// 先订阅再上报
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.IngestTelemetry(ctx)
	require.NoError(t, err)

	const total = 5
	for i := 0; i < total; i++ {
		require.NoError(t, stream.Send(&TelemetryFrame{
			SatelliteId: "grpc-sat",
			Parameters:  map[string]string{"voltage": "3.3"},
			Quality:     "good",
		}))
	}
	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)
	assert.Equal(t, int32(total), resp.Total)
	assert.Equal(t, int32(total), resp.Accepted)

	// 订阅方收到全部事件
	got := 0
	deadline := time.After(3 * time.Second)
	for got < total {
		select {
		case e := <-ch:
			assert.Equal(t, event.EventTelemetryReceived, e.EventType)
			assert.Equal(t, "grpc-sat", e.SourceNodeID)
			got++
		case <-deadline:
			t.Fatalf("gRPC 入口事件丢失: 收到 %d/%d", got, total)
		}
	}
}

// TestIngestTelemetry_GRPCReplay 验证 gRPC 客户端流上报的事件可被 Replay 读回（T5.7 入总线持久化）。
func TestIngestTelemetry_GRPCReplay(t *testing.T) {
	client, bus, cleanup := ingestTestServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.IngestTelemetry(ctx)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		require.NoError(t, stream.Send(&TelemetryFrame{
			SatelliteId: "grpc-replay",
			Quality:     "good",
		}))
	}
	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)
	assert.Equal(t, int32(3), resp.Accepted)

	// 等待事件写入完成
	time.Sleep(200 * time.Millisecond)
	replayed, err := bus.Replay(ctx, core.ReplayOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 3)
	for _, e := range replayed {
		assert.Equal(t, event.EventTelemetryReceived, e.EventType)
		assert.Equal(t, "grpc-replay", e.SourceNodeID)
	}
}