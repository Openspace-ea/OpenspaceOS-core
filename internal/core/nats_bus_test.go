package core

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/pkg/event"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats-server/v2/server"
)

// startTestNATS 启动一个嵌入式 NATS server（含 JetStream）并返回地址。
func startTestNATS(t *testing.T) string {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1, // 随机端口
		JetStream: true,
		NoLog:     true,
		NoSigs:    true,
		StoreDir:  t.TempDir(),
	}
	s, err := server.NewServer(opts)
	require.NoError(t, err)
	go s.Start()
	t.Cleanup(func() { s.Shutdown() })

	// 等待 server 就绪（ReadyForConnections 返回 bool，参数为等待时长）
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server 未就绪")
	}

	// 返回客户端地址
	tcp, ok := s.Addr().(*net.TCPAddr)
	require.True(t, ok, "NATS server 地址类型异常")
	return fmt.Sprintf("nats://%s:%d", opts.Host, tcp.Port)
}

// natsTestEvent 生成一个合法可发布的事件。
func natsTestEvent(id string, et event.EventType, nodeID string) *event.Event {
	var payload map[string]any
	switch et {
	case event.EventNodeRegistered, event.EventNodeUpdated, event.EventNodeDeleted,
		event.EventRelationshipCreated, event.EventRelationshipDeleted:
		payload = map[string]any{
			"nodeId":   nodeID,
			"nodeType": "satellite",
			"eventType": "test",
		}
	case event.EventTelemetryReceived:
		payload = map[string]any{
			"satelliteId": nodeID,
			"timestamp":   time.Now().UTC(),
			"parameters":  map[string]any{"voltage": 3.3},
			"quality":     "good",
		}
	case event.EventStateUpdated, event.EventHealthAlarm, event.EventTaskStatusChanged,
		event.EventTaskScheduled, event.EventCommandSent, event.EventCommandAcked,
		event.EventResourceMatchCompleted:
		payload = map[string]any{
			"nodeId":    nodeID,
			"newStatus": "online",
		}
	default:
		payload = map[string]any{"nodeId": nodeID}
	}
	return &event.Event{
		EventID:      id,
		EventType:    et,
		Timestamp:    time.Now().UTC(),
		SourceNodeID: nodeID,
		Payload:      payload,
	}
}

// TestNATSBus_PublishReplay 验证发布后可通过 Replay 历史读取（T4.3）。
func TestNATSBus_PublishReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-test"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	require.NoError(t, b.Publish(ctx, natsTestEvent("e1", event.EventNodeRegistered, "node-1")))
	require.NoError(t, b.Publish(ctx, natsTestEvent("e2", event.EventTelemetryReceived, "node-1")))

	// 等待 async 发布完成
	time.Sleep(300 * time.Millisecond)

	// 按类型过滤回放
	replayed, err := b.Replay(ctx, ReplayOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	require.NoError(t, err)
	assert.Len(t, replayed, 1)
	assert.Equal(t, "e1", replayed[0].EventID)
}

// TestNATSBus_Subscribe 验证实时订阅能收到新发布的事件（T4.4）。
func TestNATSBus_Subscribe(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-sub"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	ch, unsubscribe := b.Subscribe(SubscribeOptions{
		EventTypes: []event.EventType{event.EventNodeRegistered},
	})
	defer unsubscribe()

	// 等待 consumer 建立
	time.Sleep(300 * time.Millisecond)

	require.NoError(t, b.Publish(ctx, natsTestEvent("s1", event.EventNodeRegistered, "node-x")))
	require.NoError(t, b.Publish(ctx, natsTestEvent("s2", event.EventTelemetryReceived, "node-x")))

	select {
	case e := <-ch:
		assert.Equal(t, "s1", e.EventID)
	case <-time.After(5 * time.Second):
		t.Fatal("未在超时内收到订阅事件")
	}

	// s2（telemetry）不应被该订阅者收到（类型过滤）
	select {
	case e := <-ch:
		t.Fatalf("不应收到类型不符的事件: %s", e.EventID)
	case <-time.After(300 * time.Millisecond):
		// 预期
	}
}

// TestNATSBus_MultipleSubscribers 验证多订阅者各自独立接收（T4.4）。
func TestNATSBus_MultipleSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-multi"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	ch1, unsub1 := b.Subscribe(SubscribeOptions{})
	defer unsub1()
	ch2, unsub2 := b.Subscribe(SubscribeOptions{})
	defer unsub2()

	time.Sleep(300 * time.Millisecond)

	require.NoError(t, b.Publish(ctx, natsTestEvent("m1", event.EventStateUpdated, "node-y")))

	for i, ch := range []<-chan *event.Event{ch1, ch2} {
		select {
		case e := <-ch:
			assert.Equal(t, "m1", e.EventID, "订阅者 %d", i)
		case <-time.After(5 * time.Second):
			t.Fatalf("订阅者 %d 未收到事件", i)
		}
	}
}

// TestNATSBus_SchemaValidation 验证未注册/非法事件被发布前校验拒绝（T4.6）。
func TestNATSBus_SchemaValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-validate"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	// 未注册类型
	err = b.Publish(ctx, &event.Event{
		EventID:      "bad",
		EventType:    "unknown.type",
		Timestamp:    time.Now(),
		SourceNodeID: "n",
		Payload:      map[string]any{"x": 1},
	})
	assert.Error(t, err)
}

// TestNATSBus_Close 验证 Close 不阻塞且可重复调用。
func TestNATSBus_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-close"))
	require.NoError(t, err)
	require.NoError(t, b.Close())
	require.NoError(t, b.Close()) // 幂等
}

// TestNATSBus_ReplayOrdering 验证回放保持发布顺序（T4.7 顺序性）。
func TestNATSBus_ReplayOrdering(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-order"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	const n = 50
	for i := 0; i < n; i++ {
		e := natsTestEvent(fmt.Sprintf("oid-%03d", i), event.EventStateUpdated, "o-node")
		require.NoError(t, b.Publish(ctx, e))
	}
	time.Sleep(300 * time.Millisecond)

	replayed, err := b.Replay(ctx, ReplayOptions{})
	require.NoError(t, err)
	require.Len(t, replayed, n)
	for i, e := range replayed {
		assert.Equal(t, fmt.Sprintf("oid-%03d", i), e.EventID, "回放应保持发布顺序", i)
	}
}

// TestNATSBus_RestartRecovery 验证 Bus 重启后可从同一 stream 恢复读到历史（T4.7 重启恢复）。
func TestNATSBus_RestartRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	const stream = "events-restart"

	// 第一次“进程”：发布并关闭
	b1, err := NewNATSBus(url, registry, nil, WithStreamName(stream))
	require.NoError(t, err)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		require.NoError(t, b1.Publish(ctx, natsTestEvent(fmt.Sprintf("r-%d", i), event.EventNodeRegistered, "r-node")))
	}
	require.NoError(t, b1.Close())

	// 模拟服务重启：重新连接同一 stream 名，历史应仍可回放
	b2, err := NewNATSBus(url, registry, nil, WithStreamName(stream))
	require.NoError(t, err)
	defer func() { _ = b2.Close() }()
	time.Sleep(200 * time.Millisecond)

	replayed, err := b2.Replay(ctx, ReplayOptions{EventTypes: []event.EventType{event.EventNodeRegistered}})
	require.NoError(t, err)
	require.Len(t, replayed, 10)
	assert.Equal(t, "r-0", replayed[0].EventID)
	assert.Equal(t, "r-9", replayed[len(replayed)-1].EventID)
}

// TestNATSBus_ConcurrentAndBackpressure 验证并发发布 + 小缓冲（背压）下事件不丢失（T4.7 并发/背压）。
func TestNATSBus_ConcurrentAndBackpressure(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过需要 NATS 的测试")
	}
	url := startTestNATS(t)
	registry := event.NewSchemaRegistry()
	b, err := NewNATSBus(url, registry, nil, WithStreamName("events-conc"))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	ctx := context.Background()
	ch, unsubscribe := b.Subscribe(SubscribeOptions{
		EventTypes: []event.EventType{event.EventStateUpdated},
		BufferSize: 16, // 小缓冲触发背压
	})
	defer unsubscribe()
	time.Sleep(300 * time.Millisecond)

	const total = 200
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < total/4; i++ {
				_ = b.Publish(ctx, natsTestEvent(fmt.Sprintf("c-%d-%d", g, i), event.EventStateUpdated, "c-node"))
			}
		}(g)
	}
	wg.Wait()

	// 订阅者慢速收齐全部，验证背压下不丢失
	got := make(map[string]bool)
	deadline := time.After(15 * time.Second)
	for len(got) < total {
		select {
		case e := <-ch:
			got[e.EventID] = true
		case <-deadline:
			t.Fatalf("背压下事件丢失: 收到 %d/%d", len(got), total)
		}
	}
	assert.Len(t, got, total)
}

// compile-test：确保 nats 驱动可用。
var _ = nats.Conn{}