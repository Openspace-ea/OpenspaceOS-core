package event

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvent_SetPayload_GetPayload 校验 SetPayload 与 GetPayload 的互转。
func TestEvent_SetPayload_GetPayload(t *testing.T) {
	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	e := &Event{
		EventID:      "evt-001",
		EventType:    EventTelemetryReceived,
		Timestamp:    ts,
		SourceNodeID: "sat-001",
		TraceID:      "trace-001",
	}
	payload := TelemetryReceivedPayload{
		SatelliteID: "sat-001",
		Timestamp:   ts,
		Parameters:  map[string]any{"temp": 42.5, "voltage": 28.1},
		Quality:     "good",
	}

	require.NoError(t, e.SetPayload(payload))
	assert.Equal(t, "sat-001", e.Payload["satelliteId"])
	assert.Equal(t, "good", e.Payload["quality"])

	var got TelemetryReceivedPayload
	require.NoError(t, e.GetPayload(&got))
	assert.Equal(t, payload.SatelliteID, got.SatelliteID)
	assert.Equal(t, payload.Quality, got.Quality)
	assert.True(t, got.Timestamp.Equal(ts))
}

// TestEvent_SetPayload_Nil 校验 SetPayload 传入 nil 时返回错误。
func TestEvent_SetPayload_Nil(t *testing.T) {
	e := &Event{}
	err := e.SetPayload(nil)
	require.Error(t, err)
}

// TestEvent_SetPayload_GetPayload_NilTarget 校验 GetPayload 传入 nil 时返回错误。
func TestEvent_SetPayload_GetPayload_NilTarget(t *testing.T) {
	e := &Event{Payload: map[string]any{"a": 1}}
	err := e.GetPayload(nil)
	require.Error(t, err)
}

// TestPayloadSerialization 校验每个事件类型的 Payload 结构体正确序列化/反序列化。
func TestPayloadSerialization(t *testing.T) {
	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		payload any
		check   func(t *testing.T, data []byte)
	}{
		{
			name: "TelemetryReceived",
			payload: TelemetryReceivedPayload{
				SatelliteID: "sat-001",
				Timestamp:   ts,
				Parameters:  map[string]any{"temp": 42.5},
				Quality:     "good",
			},
			check: func(t *testing.T, data []byte) {
				var got TelemetryReceivedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "sat-001", got.SatelliteID)
				assert.Equal(t, "good", got.Quality)
			},
		},
		{
			name: "StateUpdated",
			payload: StateUpdatedPayload{
				NodeID:    "node-001",
				OldStatus: "Active",
				NewStatus: "Idle",
			},
			check: func(t *testing.T, data []byte) {
				var got StateUpdatedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "Idle", got.NewStatus)
			},
		},
		{
			name: "NodeRegistered",
			payload: NodeRegisteredPayload{
				NodeID:   "node-001",
				NodeType: "Satellite",
			},
			check: func(t *testing.T, data []byte) {
				var got NodeRegisteredPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "Satellite", got.NodeType)
			},
		},
		{
			name: "NodeUpdated",
			payload: NodeUpdatedPayload{
				NodeID:  "node-001",
				Changes: map[string]any{"status": "Active"},
			},
			check: func(t *testing.T, data []byte) {
				var got NodeUpdatedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "Active", got.Changes["status"])
			},
		},
		{
			name:    "NodeDeleted",
			payload: NodeDeletedPayload{NodeID: "node-001"},
			check: func(t *testing.T, data []byte) {
				var got NodeDeletedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "node-001", got.NodeID)
			},
		},
		{
			name: "TaskStatusChanged",
			payload: TaskStatusChangedPayload{
				TaskID:    "task-001",
				OldStatus: "Pending",
				NewStatus: "Running",
			},
			check: func(t *testing.T, data []byte) {
				var got TaskStatusChangedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "Running", got.NewStatus)
			},
		},
		{
			name: "TaskScheduled",
			payload: TaskScheduledPayload{
				TaskID:      "task-001",
				SatelliteID: "sat-001",
				ScheduledAt: ts,
			},
			check: func(t *testing.T, data []byte) {
				var got TaskScheduledPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "sat-001", got.SatelliteID)
				assert.True(t, got.ScheduledAt.Equal(ts))
			},
		},
		{
			name: "HealthAlarm",
			payload: HealthAlarmPayload{
				NodeID:   "node-001",
				Severity: "critical",
				Message:  "温度过高",
			},
			check: func(t *testing.T, data []byte) {
				var got HealthAlarmPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "critical", got.Severity)
			},
		},
		{
			name: "CommandSent",
			payload: CommandSentPayload{
				CommandID:   "cmd-001",
				SatelliteID: "sat-001",
				CommandType: "ATTITUDE",
			},
			check: func(t *testing.T, data []byte) {
				var got CommandSentPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "ATTITUDE", got.CommandType)
			},
		},
		{
			name: "CommandAcked",
			payload: CommandAckedPayload{
				CommandID: "cmd-001",
				Success:   true,
				Message:   "ok",
			},
			check: func(t *testing.T, data []byte) {
				var got CommandAckedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.True(t, got.Success)
				assert.Equal(t, "ok", got.Message)
			},
		},
		{
			name: "ConjunctionAlert",
			payload: ConjunctionAlertPayload{
				PrimarySatelliteID:    "sat-001",
				SecondaryObjectID:     "deb-002",
				Probability:           0.0012,
				TimeOfClosestApproach: ts,
				ImpactAssessment: ImpactAssessment{
					AffectedTasks: []string{"task-001", "task-002"},
					RiskLevel:     "high",
				},
			},
			check: func(t *testing.T, data []byte) {
				var got ConjunctionAlertPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "sat-001", got.PrimarySatelliteID)
				assert.Equal(t, 0.0012, got.Probability)
				assert.Equal(t, "high", got.ImpactAssessment.RiskLevel)
				assert.Equal(t, []string{"task-001", "task-002"}, got.ImpactAssessment.AffectedTasks)
			},
		},
		{
			name: "ResourceMatchCompleted",
			payload: ResourceMatchCompletedPayload{
				TaskID:      "task-001",
				SatelliteID: "sat-001",
				ResourceIDs: []string{"gs-001", "ant-001"},
				MatchScore:  0.92,
			},
			check: func(t *testing.T, data []byte) {
				var got ResourceMatchCompletedPayload
				require.NoError(t, json.Unmarshal(data, &got))
				assert.Equal(t, "task-001", got.TaskID)
				assert.Equal(t, 0.92, got.MatchScore)
				assert.Equal(t, []string{"gs-001", "ant-001"}, got.ResourceIDs)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			tc.check(t, data)
		})
	}
}
