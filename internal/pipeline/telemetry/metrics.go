package telemetry

import "sync/atomic"

// Metrics 记录遥测流水线的运行指标。
//
// 所有字段均使用 atomic 操作以保证并发安全，
// 可在高并发场景下无锁更新。
type Metrics struct {
	// IngressQPS 入口消息计数（由调用方按周期计算 QPS）。
	IngressQPS atomic.Int64
	// ParseLatency 最近一次解析耗时（微秒）。
	ParseLatency atomic.Int64
	// DropCount 因缓冲队列满而被丢弃的消息总数。
	DropCount atomic.Int64
}

// Snapshot 返回当前指标的快照，便于上层采集。
type MetricsSnapshot struct {
	IngressQPS   int64
	ParseLatency int64
	DropCount    int64
}

// Snapshot 获取当前指标快照。
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		IngressQPS:   m.IngressQPS.Load(),
		ParseLatency: m.ParseLatency.Load(),
		DropCount:    m.DropCount.Load(),
	}
}

// BufferQueue 是带大小限制的接收缓冲队列。
//
// 当队列已满时，Push 会丢弃最旧的数据并累加 DropCount，
// 以保证最新数据能够入队（背压策略：丢弃旧数据）。
type BufferQueue struct {
	ch      chan []byte
	maxSize int
	metrics *Metrics
}

// NewBufferQueue 创建一个指定容量的缓冲队列。
//
// maxSize 为队列容量；metrics 为 nil 时内部创建一个新 Metrics。
func NewBufferQueue(maxSize int, metrics *Metrics) *BufferQueue {
	if maxSize <= 0 {
		maxSize = 1024
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &BufferQueue{
		ch:      make(chan []byte, maxSize),
		maxSize: maxSize,
		metrics: metrics,
	}
}

// Push 向队列推入一条数据。
//
// 若队列已满，丢弃最旧的一条数据（累加 DropCount）后再推入。
func (q *BufferQueue) Push(data []byte) {
	select {
	case q.ch <- data:
		// 直接入队成功
	default:
		// 队列满，丢弃最旧数据
		select {
		case <-q.ch:
			q.metrics.DropCount.Add(1)
		default:
			// 极端情况：并发下已被其他 goroutine 取走
		}
		// 再次尝试推入（非阻塞，避免阻塞解析 goroutine）
		select {
		case q.ch <- data:
		default:
			// 仍失败则放弃本条数据并计数
			q.metrics.DropCount.Add(1)
		}
	}
}

// Pop 阻塞地从队列取出一条数据。
// 当队列关闭且无数据时，返回 nil, false。
func (q *BufferQueue) Pop() ([]byte, bool) {
	data, ok := <-q.ch
	return data, ok
}

// TryPop 非阻塞地取出一条数据。
func (q *BufferQueue) TryPop() ([]byte, bool) {
	select {
	case data := <-q.ch:
		return data, true
	default:
		return nil, false
	}
}

// Close 关闭队列，释放底层 channel。
func (q *BufferQueue) Close() {
	close(q.ch)
}

// Len 返回当前队列中未处理的数据条数。
func (q *BufferQueue) Len() int {
	return len(q.ch)
}

// Metrics 返回队列关联的指标实例。
func (q *BufferQueue) Metrics() *Metrics {
	return q.metrics
}
