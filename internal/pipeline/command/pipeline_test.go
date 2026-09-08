package command

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// newTestPipeline 创建用于测试的 Pipeline 及其依赖。
func newTestPipeline(t *testing.T) (*Pipeline, *plugin.Manager, core.MessageBus) {
	t.Helper()
	registry := event.NewSchemaRegistry()
	store := core.NewMemoryEventStore(core.StoreConfig{})
	bus := core.NewLocalBus(store, registry, nil)
	t.Cleanup(func() { _ = bus.Close() })

	broker := plugin.NewBroker(bus, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	// 注册 mock 适配器
	mgr.RegisterAdapter("mock", NewMockAdapter(10*time.Millisecond, 1.0))

	p := NewPipeline(mgr, bus, nil)
	// 设置快速调度配置便于测试
	p.SetSchedulerConfig(SchedulerConfig{
		MaxConcurrent: 5,
		Timeout:       5 * time.Second,
		MaxRetries:    1,
		RetryBackoff:  50 * time.Millisecond,
	})
	// 设置默认适配器
	require.NoError(t, p.SetAdapter("mock"))
	p.Start()
	t.Cleanup(p.Stop)
	return p, mgr, bus
}

// TestPipelineSendEndToEnd 测试指令发送闭环：Send → 收到 Ack → 验证事件。
func TestPipelineSendEndToEnd(t *testing.T) {
	p, _, bus := newTestPipeline(t)

	// 订阅事件
	ch, unsubscribe := bus.Subscribe(core.SubscribeOptions{
		EventTypes: []event.EventType{event.EventCommandSent, event.EventCommandAcked},
	})
	defer unsubscribe()

	// 发送指令
	cmd := plugin.Command{
		CommandID:   "pipeline-cmd-1",
		SatelliteID: "sat-pipeline",
		CommandType: "attitude",
		Parameters:  map[string]any{"mode": "point"},
	}
	require.NoError(t, p.Send(context.Background(), cmd, 5))

	// 等待 CommandSent 事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventCommandSent, e.EventType)
		assert.Equal(t, "sat-pipeline", e.SourceNodeID)
		var sentPayload event.CommandSentPayload
		require.NoError(t, e.GetPayload(&sentPayload))
		assert.Equal(t, "pipeline-cmd-1", sentPayload.CommandID)
		assert.Equal(t, "attitude", sentPayload.CommandType)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 CommandSent 事件超时")
	}

	// 等待 CommandAcked 事件
	select {
	case e := <-ch:
		assert.Equal(t, event.EventCommandAcked, e.EventType)
		var ackedPayload event.CommandAckedPayload
		require.NoError(t, e.GetPayload(&ackedPayload))
		assert.Equal(t, "pipeline-cmd-1", ackedPayload.CommandID)
		assert.True(t, ackedPayload.Success)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 CommandAcked 事件超时")
	}

	// 验证状态查询
	status, err := p.GetStatus("pipeline-cmd-1")
	require.NoError(t, err)
	assert.Equal(t, StatusAcked, status.Status)
	assert.Equal(t, "pipeline-cmd-1", status.CommandID)
	assert.Equal(t, "sat-pipeline", status.SatelliteID)
	assert.Equal(t, "attitude", status.CommandType)
	require.NotNil(t, status.Ack)
	assert.True(t, status.Ack.Success)
	assert.NotNil(t, status.AckedAt)
}

// TestPipelineGetStatus 测试 Send 后查询状态。
func TestPipelineGetStatus(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	cmd := plugin.Command{
		CommandID:   "status-cmd",
		SatelliteID: "sat-1",
		CommandType: "test",
	}
	require.NoError(t, p.Send(context.Background(), cmd, 3))

	// 等待处理完成
	require.Eventually(t, func() bool {
		status, err := p.GetStatus("status-cmd")
		return err == nil && status.Status == StatusAcked
	}, 5*time.Second, 50*time.Millisecond)

	status, err := p.GetStatus("status-cmd")
	require.NoError(t, err)
	assert.Equal(t, "status-cmd", status.CommandID)
	assert.Equal(t, 3, status.Priority)
}

// TestPipelineGetStatusNotFound 测试查询不存在的指令。
func TestPipelineGetStatusNotFound(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	_, err := p.GetStatus("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestPipelineCancel 测试取消待发送的指令。
func TestPipelineCancel(t *testing.T) {
	// 使用长延迟适配器，确保指令在发送前可被取消
	bus := newTestSchedulerBus(t)
	broker := plugin.NewBroker(bus, nil, nil)
	mgr := plugin.NewManager(broker, nil)
	mgr.RegisterAdapter("slow-mock", NewMockAdapter(5*time.Second, 1.0))

	p := NewPipeline(mgr, bus, nil)
	p.SetSchedulerConfig(SchedulerConfig{
		MaxConcurrent: 1, // 单并发，确保第二条指令排队等待
		Timeout:       10 * time.Second,
		MaxRetries:    0,
		RetryBackoff:  100 * time.Millisecond,
	})
	require.NoError(t, p.SetAdapter("slow-mock"))
	p.Start()
	defer p.Stop()

	// 发送第一条指令（会占用唯一的并发槽位）
	cmd1 := plugin.Command{
		CommandID:   "cancel-blocker",
		SatelliteID: "sat-1",
		CommandType: "test",
	}
	require.NoError(t, p.Send(context.Background(), cmd1, 10))

	// 等待第一条进入 sending 状态
	require.Eventually(t, func() bool {
		s, err := p.GetStatus("cancel-blocker")
		return err == nil && (s.Status == StatusSending || s.Status == StatusAcked || s.Status == StatusTimeout)
	}, 3*time.Second, 20*time.Millisecond)

	// 发送第二条指令（应处于 queued 状态等待）
	cmd2 := plugin.Command{
		CommandID:   "cancel-target",
		SatelliteID: "sat-1",
		CommandType: "test",
	}
	require.NoError(t, p.Send(context.Background(), cmd2, 5))

	// 确认第二条处于 queued
	status, err := p.GetStatus("cancel-target")
	require.NoError(t, err)
	assert.Equal(t, StatusQueued, status.Status)

	// 取消第二条
	require.NoError(t, p.Cancel("cancel-target"))

	status, err = p.GetStatus("cancel-target")
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, status.Status)
}

// TestPipelineCancelAlreadySent 测试取消已发送的指令返回错误。
func TestPipelineCancelAlreadySent(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	cmd := plugin.Command{
		CommandID:   "cancel-sent",
		SatelliteID: "sat-1",
		CommandType: "test",
	}
	require.NoError(t, p.Send(context.Background(), cmd, 5))

	// 等待指令完成
	require.Eventually(t, func() bool {
		status, _ := p.GetStatus("cancel-sent")
		return status != nil && status.Status == StatusAcked
	}, 5*time.Second, 50*time.Millisecond)

	// 已 acked 的指令不能取消
	err := p.Cancel("cancel-sent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "无法取消")
}

// TestPipelineCancelNotFound 测试取消不存在的指令。
func TestPipelineCancelNotFound(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	err := p.Cancel("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不存在")
}

// TestPipelineListCommands 测试列出所有指令。
func TestPipelineListCommands(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	cmd1 := plugin.Command{CommandID: "list-1", SatelliteID: "sat-1", CommandType: "test"}
	cmd2 := plugin.Command{CommandID: "list-2", SatelliteID: "sat-1", CommandType: "test"}
	require.NoError(t, p.Send(context.Background(), cmd1, 5))
	require.NoError(t, p.Send(context.Background(), cmd2, 5))

	// 等待处理完成
	require.Eventually(t, func() bool {
		all := p.ListCommands("")
		acked := 0
		for _, s := range all {
			if s.Status == StatusAcked {
				acked++
			}
		}
		return acked >= 2
	}, 5*time.Second, 50*time.Millisecond)

	// 列出全部
	all := p.ListCommands("")
	assert.GreaterOrEqual(t, len(all), 2)

	// 按状态过滤
	acked := p.ListCommands(StatusAcked)
	assert.GreaterOrEqual(t, len(acked), 2)
	for _, s := range acked {
		assert.Equal(t, StatusAcked, s.Status)
	}
}

// TestPipelineSetAdapter 测试切换适配器。
func TestPipelineSetAdapter(t *testing.T) {
	p, mgr, _ := newTestPipeline(t)

	// 当前为 mock
	assert.Equal(t, "mock", p.GetAdapterName())

	// 注册另一个适配器
	mgr.RegisterAdapter("mock-fast", NewMockAdapter(1*time.Millisecond, 1.0))

	// 切换
	require.NoError(t, p.SetAdapter("mock-fast"))
	assert.Equal(t, "mock-fast", p.GetAdapterName())

	// 切换回
	require.NoError(t, p.SetAdapter("mock"))
	assert.Equal(t, "mock", p.GetAdapterName())

	// 不存在的适配器
	err := p.SetAdapter("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未找到")
}

// TestPipelineListAdapters 测试列出可用适配器。
func TestPipelineListAdapters(t *testing.T) {
	p, mgr, _ := newTestPipeline(t)
	mgr.RegisterAdapter("mock-fast", NewMockAdapter(1*time.Millisecond, 1.0))

	adapters := p.ListAdapters()
	assert.Contains(t, adapters, "mock")
	assert.Contains(t, adapters, "mock-fast")
}

// TestPipelineAutoGenerateCommandID 测试 CommandID 为空时自动生成。
func TestPipelineAutoGenerateCommandID(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	cmd := plugin.Command{
		CommandID:   "", // 空 ID，应自动生成
		SatelliteID: "sat-1",
		CommandType: "test",
	}
	require.NoError(t, p.Send(context.Background(), cmd, 5))

	// 查询队列中的指令，应有自动生成的 ID
	all := p.ListCommands("")
	require.GreaterOrEqual(t, len(all), 1)
	assert.NotEmpty(t, all[0].CommandID)
}

// TestPipelineSendInvalidCommand 测试发送无效指令返回错误。
func TestPipelineSendInvalidCommand(t *testing.T) {
	p, _, _ := newTestPipeline(t)

	// 空 SatelliteID
	err := p.Send(context.Background(), plugin.Command{
		CommandID:   "invalid-1",
		SatelliteID: "",
		CommandType: "test",
	}, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "satelliteId")

	// 空 CommandType
	err = p.Send(context.Background(), plugin.Command{
		CommandID:   "invalid-2",
		SatelliteID: "sat-1",
		CommandType: "",
	}, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commandType")
}

// TestValidatorValidate 测试校验器各字段校验。
func TestValidatorValidate(t *testing.T) {
	v := NewValidator(nil)

	// nil command
	err := v.Validate(nil)
	require.Error(t, err)

	// 空 CommandID
	err = v.Validate(&plugin.Command{
		CommandID:   "",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commandId")

	// 空 SatelliteID
	err = v.Validate(&plugin.Command{
		CommandID:   "cmd-1",
		SatelliteID: "",
		CommandType: "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "satelliteId")

	// 空 CommandType
	err = v.Validate(&plugin.Command{
		CommandID:   "cmd-1",
		SatelliteID: "sat-1",
		CommandType: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commandType")

	// 合法指令
	err = v.Validate(&plugin.Command{
		CommandID:   "cmd-1",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	require.NoError(t, err)
}

// TestMockAdapter 测试 Mock 适配器成功/失败场景。
func TestMockAdapter(t *testing.T) {
	// 成功场景
	successAdapter := NewMockAdapter(1*time.Millisecond, 1.0)
	assert.Equal(t, "mock", successAdapter.Name())

	ack, err := successAdapter.Send(plugin.Command{
		CommandID:   "mock-1",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	require.NoError(t, err)
	assert.True(t, ack.Success)

	// 失败场景（成功率 0）
	failAdapter := NewMockAdapter(1*time.Millisecond, 0)
	ack, err = failAdapter.Send(plugin.Command{
		CommandID:   "mock-2",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	require.NoError(t, err) // Mock 适配器返回 Ack 而非 error
	assert.False(t, ack.Success)

	// 默认值
	defaultAdapter := NewMockAdapter(0, -1)
	assert.Equal(t, "mock", defaultAdapter.Name())
	ack, err = defaultAdapter.Send(plugin.Command{
		CommandID:   "mock-3",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	require.NoError(t, err)
	assert.True(t, ack.Success)
}

// TestMockAdapterSetConfig 测试运行时修改 Mock 适配器配置。
func TestMockAdapterSetConfig(t *testing.T) {
	adapter := NewMockAdapter(1*time.Millisecond, 1.0)

	// 改为失败
	adapter.SetSuccessRate(0)
	ack, _ := adapter.Send(plugin.Command{
		CommandID:   "mock-config",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	assert.False(t, ack.Success)

	// 改为成功
	adapter.SetSuccessRate(1.0)
	ack, _ = adapter.Send(plugin.Command{
		CommandID:   "mock-config",
		SatelliteID: "sat-1",
		CommandType: "test",
	})
	assert.True(t, ack.Success)
}

// TestMetricsSnapshot 测试 Metrics 快照。
func TestMetricsSnapshot(t *testing.T) {
	m := &Metrics{}
	m.TotalSent.Add(10)
	m.TotalAcked.Add(8)
	m.TotalFailed.Add(1)
	m.TotalTimeout.Add(1)
	m.SendLatency.Store(500)
	m.SuccessRate.Store(8000) // 80%

	snap := m.Snapshot()
	assert.Equal(t, int64(10), snap.TotalSent)
	assert.Equal(t, int64(8), snap.TotalAcked)
	assert.Equal(t, int64(1), snap.TotalFailed)
	assert.Equal(t, int64(1), snap.TotalTimeout)
	assert.Equal(t, int64(500), snap.SendLatency)
	assert.Equal(t, int64(8000), snap.SuccessRate)
}

// TestMetricsRecordAck 测试 RecordAck 更新成功率。
func TestMetricsRecordAck(t *testing.T) {
	m := &Metrics{}
	m.TotalSent.Store(4)

	m.RecordAck(true)
	m.RecordAck(true)
	m.RecordAck(false) // 失败确认不计入 TotalAcked

	// 2 次成功确认，成功率 = 2*10000/4 = 5000 (50%)
	snap := m.Snapshot()
	assert.Equal(t, int64(2), snap.TotalAcked)
	assert.Equal(t, int64(5000), snap.SuccessRate)
}
