package usage

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// 默认聚合时间桶与落库周期。
const (
	// DefaultBucketInterval 是单个计量时间桶的长度。
	DefaultBucketInterval = time.Minute
	// DefaultFlushInterval 是聚合结果批量落库的周期。
	DefaultFlushInterval = 5 * time.Second
	// DefaultQueueCapacity 是采集缓冲队列容量（背压上限）。
	DefaultQueueCapacity = 10000
)

// Collector 负责聚合用量并异步落库（T3.4）。
//
// 模型：
//   - 请求路径只需调用 Record() 投递一个计量快照，绝不阻塞调用方；
//   - 后台 goroutine 周期性地把缓冲中的 Record 按 (tenant,client,
//     endpoint,operation,unit,时间桶) 键聚合计数，再批量写入 usage 表；
//   - 当缓冲队列满时（背压），采用丢弃并累计 drop 计数，避免内存无限增长。
type Collector struct {
	store  Store
	logger *slog.Logger

	// queue 是采集缓冲通道。Record 非阻塞投递；后台 goroutine 周期清空。
	queue chan Record

	mu      sync.Mutex
	dropped int64 // 因背压丢弃的条数
	flushed int64 // 成功落库的聚合桶数

	done chan struct{}
	wg   sync.WaitGroup

	bucketInterval time.Duration
	flushInterval  time.Duration
}

// CollectorConfig 配置 Collector。
type CollectorConfig struct {
	BucketInterval time.Duration
	FlushInterval  time.Duration
	QueueCapacity  int
}

// applyDefaults 填充未设置项为默认值。
func (c CollectorConfig) applyDefaults() CollectorConfig {
	if c.BucketInterval <= 0 {
		c.BucketInterval = DefaultBucketInterval
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = DefaultFlushInterval
	}
	if c.QueueCapacity <= 0 {
		c.QueueCapacity = DefaultQueueCapacity
	}
	return c
}

// NewCollector 创建 Collector 并启动后台聚合 goroutine。
func NewCollector(store Store, cfg CollectorConfig, logger *slog.Logger) *Collector {
	if logger == nil {
		logger = slog.Default()
	}
	if store == nil {
		logger.Warn("store 为 nil，Collector 仍启动但落库为空操作")
	}
	cfg = cfg.applyDefaults()
	c := &Collector{
		store:          store,
		logger:         logger,
		queue:          make(chan Record, cfg.QueueCapacity),
		done:           make(chan struct{}),
		bucketInterval: cfg.BucketInterval,
		flushInterval:  cfg.FlushInterval,
	}
	c.wg.Add(1)
	go c.run()
	return c
}

// Record 投递一条计量快照到缓冲队列。
//
// 采用非阻塞 + 丢弃策略：队列满时直接丢弃并累计 drop 计数，绝不阻塞采集路径。
func (c *Collector) Record(ctx context.Context, r Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.queue <- r:
		return nil
	default:
		c.mu.Lock()
		c.dropped++
		c.mu.Unlock()
		return nil
	}
}

// run 后台循环：周期性清空缓冲聚合并落库。
func (c *Collector) run() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			c.flushAll()
			return
		case <-ticker.C:
			c.flushAll()
		}
	}
}

// flushAll 将当前缓冲通道中的记录全部取出、聚合并落库。
func (c *Collector) flushAll() {
	var pending []Record
collect:
	for {
		select {
		case r := <-c.queue:
			pending = append(pending, r)
		default:
			break collect
		}
	}
	if len(pending) == 0 {
		return
	}

	buckets := aggregate(pending, c.bucketInterval)
	if len(buckets) == 0 {
		return
	}
	if c.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.flushInterval)
	defer cancel()
	n, err := c.store.InsertBatch(ctx, buckets)
	if err != nil {
		c.logger.Error("usage 批量落库失败", "error", err)
		return
	}
	c.mu.Lock()
	c.flushed += n
	c.mu.Unlock()
}

// aggregate 将原始记录按计量键聚合成 Bucket 列表。
//
// 时间桶按 At 对齐到 bucketInterval 的起始边界。
func aggregate(records []Record, bucketInterval time.Duration) []Bucket {
	type key struct {
		tenant, client, endpoint, operation string
		unit                                 Unit
		bucketStart, bucketEnd               time.Time
	}
	agg := make(map[key]int64)
	for _, r := range records {
		start := r.At.Truncate(bucketInterval)
		end := start.Add(bucketInterval)
		k := key{
			tenant:      r.TenantID,
			client:      r.ClientID,
			endpoint:    r.Endpoint,
			operation:   r.Operation,
			unit:        r.Unit,
			bucketStart: start,
			bucketEnd:   end,
		}
		agg[k] += r.Count
	}
	out := make([]Bucket, 0, len(agg))
	for k, count := range agg {
		out = append(out, Bucket{
			TenantID:    k.tenant,
			ClientID:    k.client,
			Endpoint:    k.endpoint,
			Operation:   k.operation,
			Unit:        k.unit,
			Count:       count,
			BucketStart: k.bucketStart,
			BucketEnd:   k.bucketEnd,
		})
	}
	return out
}

// Flush 手动触发一次聚合落库（测试/关闭前使用）。
func (c *Collector) Flush() {
	c.flushAll()
}

// Close 停止后台 goroutine 并执行最后一次落库。
func (c *Collector) Close() error {
	close(c.done)
	c.wg.Wait()
	return nil
}