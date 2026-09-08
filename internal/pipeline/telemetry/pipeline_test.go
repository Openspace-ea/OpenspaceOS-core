package telemetry

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// freePort 获取一个可用的 TCP 端口（用于测试）。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// newTestPipeline 创建用于测试的 Pipeline 及其依赖。
func newTestPipeline(t *testing.T) (*Pipeline, *plugin.Manager, core.MessageBus) {
	t.Helper()
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })

	broker := plugin.NewBroker(bus, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	mgr.RegisterParser("json", &JSONParser{})
	mgr.RegisterParser("tle", &TLEParser{})

	p := NewPipeline(mgr, bus, nil)
	t.Cleanup(func() { _ = p.Stop() })
	return p, mgr, bus
}

// TestPipelineSetParser 测试解析器切换。
func TestPipelineSetParser(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	// 默认设置 json
	require.NoError(t, p.SetParser("json"))
	assert.Equal(t, "json", p.GetParserName())

	// 切换到 tle
	require.NoError(t, p.SetParser("tle"))
	assert.Equal(t, "tle", p.GetParserName())

	// 切换回 json
	require.NoError(t, p.SetParser("json"))
	assert.Equal(t, "json", p.GetParserName())

	// 不存在的解析器
	err := p.SetParser("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未找到")
}

// TestPipelineEndToEndJSON 启动 Pipeline → 发送 TCP JSON 数据 → 验证事件发布。
func TestPipelineEndToEndJSON(t *testing.T) {
	p, _, bus := newTestPipeline(t)
	require.NoError(t, p.SetParser("json"))

	// 订阅 TelemetryReceived 事件
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	// 启动 Pipeline
	port := freePort(t)
	ctx := context.Background()
	require.NoError(t, p.Start(ctx, port))

	// 通过 TCP 发送 JSON 行
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	require.NoError(t, err)
	defer conn.Close()

	jsonLine := `{"satelliteId":"sat-e2e","timestamp":"2026-07-01T00:00:00Z","parameters":{"temp":45.2},"quality":"good"}` + "\n"
	_, err = conn.Write([]byte(jsonLine))
	require.NoError(t, err)

	// 等待事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventTelemetryReceived, e.EventType)
		assert.Equal(t, "sat-e2e", e.SourceNodeID)
		assert.NotEmpty(t, e.EventID)
		assert.False(t, e.Timestamp.IsZero())

		// 验证 Payload
		var payload event.TelemetryReceivedPayload
		require.NoError(t, e.GetPayload(&payload))
		assert.Equal(t, "sat-e2e", payload.SatelliteID)
		assert.Equal(t, "good", payload.Quality)
		assert.Equal(t, "2026-07-01T00:00:00Z", payload.Timestamp.Format(time.RFC3339))

		// 验证 logicalShard
		shard, ok := e.Payload["logicalShard"]
		require.True(t, ok)
		shardMap, ok := shard.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "sat-e2e", shardMap["satelliteId"])
	case <-time.After(3 * time.Second):
		t.Fatal("等待遥测事件超时")
	}
}

// TestPipelineTLEViaProcess 通过 Process 方法发送 TLE 数据 → 验证事件发布。
//
// TLE 为多行格式，TCP Receiver 按行分发不适合 TLE；
// 此处通过 Process（REST /telemetry/send 共用路径）注入完整 TLE 文本。
func TestPipelineTLEViaProcess(t *testing.T) {
	p, _, bus := newTestPipeline(t)
	require.NoError(t, p.SetParser("tle"))

	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	ctx := context.Background()
	tleData := []byte(issTLELine1 + "\n" + issTLELine2)
	require.NoError(t, p.Process(ctx, tleData))

	select {
	case e := <-ch:
		assert.Equal(t, event.EventTelemetryReceived, e.EventType)
		assert.Equal(t, "25544", e.SourceNodeID)

		var payload event.TelemetryReceivedPayload
		require.NoError(t, e.GetPayload(&payload))
		assert.Equal(t, "25544", payload.SatelliteID)
	case <-time.After(3 * time.Second):
		t.Fatal("等待遥测事件超时")
	}
}

// TestPipelineProcessManual 测试通过 Process 方法手动发送数据。
func TestPipelineProcessManual(t *testing.T) {
	p, _, bus := newTestPipeline(t)
	require.NoError(t, p.SetParser("json"))

	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventTelemetryReceived},
	})
	defer unsubscribe()

	ctx := context.Background()
	jsonLine := []byte(`{"satelliteId":"sat-manual","timestamp":"2026-07-01T00:00:00Z","parameters":{"v":1},"quality":"good"}`)
	require.NoError(t, p.Process(ctx, jsonLine))

	select {
	case e := <-ch:
		assert.Equal(t, "sat-manual", e.SourceNodeID)
	case <-time.After(2 * time.Second):
		t.Fatal("等待遥测事件超时")
	}

	// 验证 metrics 更新
	snap := p.Metrics().Snapshot()
	assert.Greater(t, snap.IngressQPS, int64(0))
}

// TestPipelineProcessEmpty 测试空数据返回错误。
func TestPipelineProcessEmpty(t *testing.T) {
	p, _, _ := newTestPipeline(t)
	require.NoError(t, p.SetParser("json"))

	err := p.Process(context.Background(), []byte(""))
	require.Error(t, err)
}

// TestBufferQueueDropOnFull 测试缓冲队列满时丢弃并记录 metric。
func TestBufferQueueDropOnFull(t *testing.T) {
	metrics := &Metrics{}
	q := NewBufferQueue(2, metrics)
	defer q.Close()

	// 推入 5 条数据，容量仅 2，应丢弃 3 条
	for i := 0; i < 5; i++ {
		q.Push([]byte{byte(i)})
	}

	// DropCount 应为 3
	assert.Equal(t, int64(3), metrics.DropCount.Load())

	// 队列中应有 2 条数据（最新两条中的两条）
	assert.Equal(t, 2, q.Len())

	// 取出数据验证非空
	data, ok := q.TryPop()
	require.True(t, ok)
	assert.NotEmpty(t, data)
}

// TestBufferQueueNormal 测试缓冲队列正常推入与取出。
func TestBufferQueueNormal(t *testing.T) {
	q := NewBufferQueue(10, nil)
	defer q.Close()

	q.Push([]byte("a"))
	q.Push([]byte("b"))

	assert.Equal(t, 2, q.Len())

	data, ok := q.Pop()
	require.True(t, ok)
	assert.Equal(t, "a", string(data))

	data, ok = q.Pop()
	require.True(t, ok)
	assert.Equal(t, "b", string(data))
}

// TestBufferQueueDefaultMetrics 测试 metrics 为 nil 时内部创建。
func TestBufferQueueDefaultMetrics(t *testing.T) {
	q := NewBufferQueue(1, nil)
	defer q.Close()
	require.NotNil(t, q.Metrics())

	q.Push([]byte("a"))
	q.Push([]byte("b")) // 满，丢弃旧的
	assert.Greater(t, q.Metrics().DropCount.Load(), int64(0))
}

// TestNormalize 测试 Normalizer 标准化逻辑。
func TestNormalize(t *testing.T) {
	frame := &plugin.TelemetryFrame{
		SatelliteID: "sat-norm",
		Timestamp:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		Parameters:  map[string]any{"temp": 50.0},
		Quality:     "good",
	}

	e, err := Normalize(frame)
	require.NoError(t, err)
	assert.Equal(t, event.EventTelemetryReceived, e.EventType)
	assert.Equal(t, "sat-norm", e.SourceNodeID)
	assert.NotEmpty(t, e.EventID)
	assert.False(t, e.Timestamp.IsZero())

	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.Equal(t, "sat-norm", payload.SatelliteID)
	assert.Equal(t, "good", payload.Quality)

	// 验证 logicalShard
	shard, ok := e.Payload["logicalShard"]
	require.True(t, ok)
	shardMap := shard.(map[string]any)
	assert.Equal(t, "sat-norm", shardMap["satelliteId"])
}

// TestNormalizeNilFrame 测试 nil frame 返回错误。
func TestNormalizeNilFrame(t *testing.T) {
	_, err := Normalize(nil)
	require.Error(t, err)
}

// TestNormalizeEmptySatelliteID 测试空 SatelliteID 返回错误。
func TestNormalizeEmptySatelliteID(t *testing.T) {
	frame := &plugin.TelemetryFrame{}
	_, err := Normalize(frame)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SatelliteID")
}

// TestNormalizeNilParameters 测试 nil Parameters 时填充空 map。
func TestNormalizeNilParameters(t *testing.T) {
	frame := &plugin.TelemetryFrame{
		SatelliteID: "sat-1",
		Timestamp:   time.Now(),
	}
	e, err := Normalize(frame)
	require.NoError(t, err)

	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.NotNil(t, payload.Parameters)
	assert.Equal(t, "unknown", payload.Quality)
}

// TestNormalizeTLEFrame 测试对 TLE 帧标准化后 orbit 字段保留。
func TestNormalizeTLEFrame(t *testing.T) {
	parser := &TLEParser{}
	frames, err := parser.Parse([]byte(issTLELine1 + "\n" + issTLELine2))
	require.NoError(t, err)
	require.Len(t, frames, 1)

	e, err := Normalize(&frames[0])
	require.NoError(t, err)

	var payload event.TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&payload))
	assert.Equal(t, "25544", payload.SatelliteID)

	// orbit 经 JSON 往返后为 map
	orbitAny, ok := payload.Parameters["orbit"]
	require.True(t, ok)
	orbitMap, ok := orbitAny.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "25544", orbitMap["noradId"])
}

// TestReceiverStartNilHandler 测试 Receiver.Start 传入 nil handler 返回错误。
func TestReceiverStartNilHandler(t *testing.T) {
	r := NewReceiver(nil)
	err := r.Start(0, nil)
	require.Error(t, err)
}

// TestReceiverStopWithoutStart 测试未启动时 Stop 不 panic。
func TestReceiverStopWithoutStart(t *testing.T) {
	r := NewReceiver(nil)
	require.NoError(t, r.Stop())
}

// TestMetricsSnapshot 测试 Metrics 快照。
func TestMetricsSnapshot(t *testing.T) {
	m := &Metrics{}
	m.IngressQPS.Add(5)
	m.ParseLatency.Store(100)
	m.DropCount.Add(2)

	snap := m.Snapshot()
	assert.Equal(t, int64(5), snap.IngressQPS)
	assert.Equal(t, int64(100), snap.ParseLatency)
	assert.Equal(t, int64(2), snap.DropCount)
}
