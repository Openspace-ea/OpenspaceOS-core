package model

import "time"

// MissionProperties 任务编排节点的特有属性。
type MissionProperties struct {
	// MissionType 任务编排类型。
	MissionType string `json:"missionType"`
	// StartTime 开始时间。
	StartTime time.Time `json:"startTime"`
	// EndTime 结束时间。
	EndTime time.Time `json:"endTime"`
}
