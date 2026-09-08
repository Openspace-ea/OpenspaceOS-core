package model

import "time"

// TimeWindow 时间窗口。
type TimeWindow struct {
	// Start 起始时间。
	Start time.Time `json:"start"`
	// End 结束时间。
	End time.Time `json:"end"`
}

// TaskProperties 任务节点的特有属性。
type TaskProperties struct {
	// TaskType 任务类型。
	TaskType string `json:"taskType"`
	// Priority 优先级。
	Priority int `json:"priority"`
	// TimeWindow 时间窗口。
	TimeWindow TimeWindow `json:"timeWindow"`
}
