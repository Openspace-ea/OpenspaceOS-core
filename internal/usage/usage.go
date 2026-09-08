// Package usage 实现 Openspace OS Core 的用量统计与计费预留。
//
// 设计宗旨（Task 3.6）：仅定义"计量口径 + usage 表 + 聚合查询 API"，
// 不实现费率、账单、扣费等计费逻辑，未来演进不破坏现有 schema。
package usage

import (
	"context"
	"time"
)

// Unit 是计量单位（metering unit）的标识。
//
// 当前支持的最小计量单位，后续可按需扩展。
type Unit string

// 支持的计量单位常量。
const (
	// UnitCall 表示一次 API 调用（HTTP / gRPC）。
	UnitCall Unit = "call"
	// UnitEvent 表示发布的一个事件。
	UnitEvent Unit = "event"
	// UnitByte 表示遥测上报的字节数。
	UnitByte Unit = "byte"
	// UnitFrame 表示遥测上报的一帧/一条记录。
	UnitFrame Unit = "frame"
	// UnitNode 表示创建的 Node 数量。
	UnitNode Unit = "node"
	// UnitSubscribeSec 表示订阅连接时长（秒）。
	UnitSubscribeSec Unit = "subscribe_sec"
)

// ValidUnit 判断单位是否合法，用于入参校验。
func ValidUnit(u Unit) bool {
	switch u {
	case UnitCall, UnitEvent, UnitByte, UnitFrame, UnitNode, UnitSubscribeSec:
		return true
	}
	return false
}

// Bucket 是一次计量记录（usage 表中的一行）。
//
// 表示在 [BucketStart, BucketEnd) 时间桶内，某租户/客户端对某端点执行某
// 操作的累计 count。count>=0 的幂等累加由采集层保证。
type Bucket struct {
	ID          string    `json:"id,omitempty"`
	TenantID    string    `json:"tenantId"`
	ClientID    string    `json:"clientId"`
	Endpoint    string    `json:"endpoint"`
	Operation   string    `json:"operation"`
	Unit        Unit      `json:"unit"`
	Count       int64     `json:"count"`
	BucketStart time.Time `json:"bucketStart"`
	BucketEnd   time.Time `json:"bucketEnd"`
}

// Record 是一次需要计量的调用快照（采集中间件产生）。
//
// 采集中间件解析主体（tenant/client）后生成 Record，交给 Collector 聚合。
type Record struct {
	TenantID  string
	ClientID  string
	Endpoint  string
	Operation string
	Unit      Unit
	Count     int64
	At        time.Time
}

// Query 是 usage 聚合查询条件（T3.5 查询接口）。
type Query struct {
	TenantID  string
	ClientID  string
	Operation string
	Unit      Unit
	// HasUnit 标记是否显式指定 Unit（Unit 是字符串区分零值）。
	HasUnit bool
	Start   time.Time
	End     time.Time
}

// Aggregate 是聚合结果（按时间桶与租户/客户端分组）。
type Aggregate struct {
	TenantID    string `json:"tenantId"`
	ClientID    string `json:"clientId"`
	Endpoint    string `json:"endpoint"`
	Operation   string `json:"operation"`
	Unit        Unit   `json:"unit"`
	TotalCount  int64  `json:"totalCount"`
	BucketStart time.Time `json:"bucketStart"`
	BucketEnd   time.Time `json:"bucketEnd"`
}

// Store 是 usage 的持久化抽象。
//
// 提供插入/聚合查询能力。可与 SQLite / PostgreSQL 实现互斥。
type Store interface {
	// InsertBatch 批量写入聚合后的用量记录，返回写入条数。
	InsertBatch(ctx context.Context, buckets []Bucket) (int64, error)
	// AggregateByTenant 按租户/客户端/端点/操作聚合查询用量。
	AggregateByTenant(ctx context.Context, q Query) ([]Aggregate, error)
	// Close 释放资源（可为空操作）。
	Close() error
}