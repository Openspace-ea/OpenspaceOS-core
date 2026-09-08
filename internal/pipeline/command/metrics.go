package command

import "sync/atomic"

// Metrics 记录指令流水线的运行指标。
//
// 所有字段均使用 atomic 操作以保证并发安全。
type Metrics struct {
	// TotalSent 已发送的指令总数（含重试）。
	TotalSent atomic.Int64
	// TotalAcked 已收到确认的指令总数。
	TotalAcked atomic.Int64
	// TotalFailed 发送失败的指令总数。
	TotalFailed atomic.Int64
	// TotalTimeout 发送超时的指令总数。
	TotalTimeout atomic.Int64
	// SendLatency 最近一次发送到确认的耗时（微秒）。
	SendLatency atomic.Int64
	// SuccessRate 成功率百分比 * 100（如 95.5% 存为 9550）。
	SuccessRate atomic.Int64
}

// MetricsSnapshot 是 Metrics 的快照，便于上层采集。
type MetricsSnapshot struct {
	TotalSent    int64 `json:"totalSent"`
	TotalAcked   int64 `json:"totalAcked"`
	TotalFailed  int64 `json:"totalFailed"`
	TotalTimeout int64 `json:"totalTimeout"`
	SendLatency  int64 `json:"sendLatencyUs"`
	SuccessRate  int64 `json:"successRate"` // 百分比 * 100
}

// Snapshot 返回当前指标的快照。
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		TotalSent:    m.TotalSent.Load(),
		TotalAcked:   m.TotalAcked.Load(),
		TotalFailed:  m.TotalFailed.Load(),
		TotalTimeout: m.TotalTimeout.Load(),
		SendLatency:  m.SendLatency.Load(),
		SuccessRate:  m.SuccessRate.Load(),
	}
}

// RecordAck 记录一次确认结果并更新成功率。
//
// 仅成功确认计入 TotalAcked 与 SuccessRate。
func (m *Metrics) RecordAck(success bool) {
	if !success {
		return
	}
	m.TotalAcked.Add(1)
	sent := m.TotalSent.Load()
	if sent > 0 {
		m.SuccessRate.Store(m.TotalAcked.Load() * 10000 / sent)
	}
}

// RecordLatency 记录发送到确认的耗时。
func (m *Metrics) RecordLatency(microseconds int64) {
	m.SendLatency.Store(microseconds)
}
