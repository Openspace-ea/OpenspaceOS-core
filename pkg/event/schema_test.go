package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validEvent 返回一个合法事件，类型与 payload 由参数指定。
func validEvent(et EventType, payload any) *Event {
	e := &Event{
		EventID:      "evt-001",
		EventType:    et,
		Timestamp:    time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		SourceNodeID: "sat-001",
		TraceID:      "trace-001",
	}
	if payload != nil {
		_ = e.SetPayload(payload)
	}
	return e
}

// TestSchemaRegistry_AutoRegistered 校验 14 个事件类型默认全部已注册。
func TestSchemaRegistry_AutoRegistered(t *testing.T) {
	r := NewSchemaRegistry()
	expected := []EventType{
		EventTelemetryReceived,
		EventStateUpdated,
		EventNodeRegistered,
		EventNodeUpdated,
		EventNodeDeleted,
		EventRelationshipCreated,
		EventRelationshipDeleted,
		EventTaskStatusChanged,
		EventTaskScheduled,
		EventHealthAlarm,
		EventCommandSent,
		EventCommandAcked,
		EventConjunctionAlert,
		EventResourceMatchCompleted,
	}
	for _, et := range expected {
		s, ok := r.GetSchema(et)
		require.True(t, ok, "事件类型 %s 应已注册", et)
		assert.Equal(t, "1.0.0", s.Version)
		assert.NotEmpty(t, s.Fields, "事件类型 %s 应有字段定义", et)
	}
}

// TestSchemaRegistry_ListSchemas 校验列表返回全部 14 个 Schema 且按名称排序。
func TestSchemaRegistry_ListSchemas(t *testing.T) {
	r := NewSchemaRegistry()
	list := r.ListSchemas()
	assert.Len(t, list, 14)
	for i := 1; i < len(list); i++ {
		assert.True(t, string(list[i-1].EventType) <= string(list[i].EventType),
			"ListSchemas 应按事件类型名称排序")
	}
}

// TestSchemaRegistry_GetSchema_NotFound 校验未注册类型查询返回 false。
func TestSchemaRegistry_GetSchema_NotFound(t *testing.T) {
	r := NewSchemaRegistry()
	_, ok := r.GetSchema(EventType("Unknown"))
	assert.False(t, ok)
}

// TestSchemaRegistry_Validate_OK 校验各类事件通过校验。
func TestSchemaRegistry_Validate_OK(t *testing.T) {
	r := NewSchemaRegistry()
	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		event   *Event
	}{
		{
			name: "TelemetryReceived",
			event: validEvent(EventTelemetryReceived, TelemetryReceivedPayload{
				SatelliteID: "sat-001",
				Timestamp:   ts,
				Parameters:  map[string]any{"temp": 1.0},
				Quality:     "good",
			}),
		},
		{
			name: "ConjunctionAlert",
			event: validEvent(EventConjunctionAlert, ConjunctionAlertPayload{
				PrimarySatelliteID:    "sat-001",
				SecondaryObjectID:     "deb-002",
				Probability:           0.01,
				TimeOfClosestApproach: ts,
				ImpactAssessment:      ImpactAssessment{AffectedTasks: []string{"t1"}, RiskLevel: "high"},
			}),
		},
		{
			name: "ResourceMatchCompleted",
			event: validEvent(EventResourceMatchCompleted, ResourceMatchCompletedPayload{
				TaskID: "task-001", SatelliteID: "sat-001",
				ResourceIDs: []string{"gs-001"}, MatchScore: 0.9,
			}),
		},
		{
			name: "StateUpdated",
			event: validEvent(EventStateUpdated, StateUpdatedPayload{
				NodeID: "node-001", OldStatus: "Active", NewStatus: "Idle",
			}),
		},
		{
			name: "NodeRegistered",
			event: validEvent(EventNodeRegistered, NodeRegisteredPayload{
				NodeID: "node-001", NodeType: "Satellite",
			}),
		},
		{
			name: "NodeUpdated",
			event: validEvent(EventNodeUpdated, NodeUpdatedPayload{
				NodeID: "node-001", Changes: map[string]any{"status": "Active"},
			}),
		},
		{
			name:  "NodeDeleted",
			event: validEvent(EventNodeDeleted, NodeDeletedPayload{NodeID: "node-001"}),
		},
		{
			name: "TaskStatusChanged",
			event: validEvent(EventTaskStatusChanged, TaskStatusChangedPayload{
				TaskID: "task-001", OldStatus: "Pending", NewStatus: "Running",
			}),
		},
		{
			name: "TaskScheduled",
			event: validEvent(EventTaskScheduled, TaskScheduledPayload{
				TaskID: "task-001", SatelliteID: "sat-001", ScheduledAt: ts,
			}),
		},
		{
			name: "HealthAlarm",
			event: validEvent(EventHealthAlarm, HealthAlarmPayload{
				NodeID: "node-001", Severity: "critical", Message: "高温",
			}),
		},
		{
			name: "CommandSent",
			event: validEvent(EventCommandSent, CommandSentPayload{
				CommandID: "cmd-001", SatelliteID: "sat-001", CommandType: "ATTITUDE",
			}),
		},
		{
			name: "CommandAcked",
			event: validEvent(EventCommandAcked, CommandAckedPayload{
				CommandID: "cmd-001", Success: true, Message: "ok",
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NoError(t, r.Validate(tc.event))
		})
	}
}

// TestSchemaRegistry_Validate_MissingRequired 校验缺少必填字段时校验失败。
func TestSchemaRegistry_Validate_MissingRequired(t *testing.T) {
	r := NewSchemaRegistry()
	e := validEvent(EventTelemetryReceived, TelemetryReceivedPayload{
		SatelliteID: "sat-001",
		Timestamp:   time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Parameters:  map[string]any{"temp": 1.0},
		Quality:     "good",
	})
	// 删除必填字段 satelliteId
	delete(e.Payload, "satelliteId")
	err := r.Validate(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "satelliteId")
}

// TestSchemaRegistry_Validate_EmptyRequired 校验必填字段为空字符串时校验失败。
func TestSchemaRegistry_Validate_EmptyRequired(t *testing.T) {
	r := NewSchemaRegistry()
	e := validEvent(EventTelemetryReceived, TelemetryReceivedPayload{
		SatelliteID: "",
		Timestamp:   time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Parameters:  map[string]any{"temp": 1.0},
		Quality:     "good",
	})
	err := r.Validate(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "satelliteId")
}

// TestSchemaRegistry_Validate_HeaderErrors 校验事件头缺失时返回错误。
func TestSchemaRegistry_Validate_HeaderErrors(t *testing.T) {
	r := NewSchemaRegistry()
	base := validEvent(EventNodeDeleted, NodeDeletedPayload{NodeID: "node-001"})

	cases := []struct {
		name    string
		mutate  func(*Event)
		wantErr string
	}{
		{"eventId 为空", func(e *Event) { e.EventID = "" }, "eventId"},
		{"eventType 为空", func(e *Event) { e.EventType = "" }, "eventType"},
		{"sourceNodeId 为空", func(e *Event) { e.SourceNodeID = "" }, "sourceNodeId"},
		{"timestamp 为零值", func(e *Event) { e.Timestamp = time.Time{} }, "timestamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := *base
			tc.mutate(&e)
			err := r.Validate(&e)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestSchemaRegistry_Validate_UnknownType 校验未注册事件类型返回错误。
func TestSchemaRegistry_Validate_UnknownType(t *testing.T) {
	r := NewSchemaRegistry()
	e := validEvent(EventType("Unknown"), NodeDeletedPayload{NodeID: "node-001"})
	err := r.Validate(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未注册")
}

// TestSchemaRegistry_Validate_NilPayload 校验 payload 为 nil 时返回错误。
func TestSchemaRegistry_Validate_NilPayload(t *testing.T) {
	r := NewSchemaRegistry()
	e := &Event{
		EventID:      "evt-001",
		EventType:    EventNodeDeleted,
		Timestamp:    time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		SourceNodeID: "sat-001",
		TraceID:      "trace-001",
		Payload:      nil,
	}
	err := r.Validate(e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload")
}

// TestSchemaRegistry_Register_Custom 校验自定义注册可覆盖默认 Schema。
func TestSchemaRegistry_Register_Custom(t *testing.T) {
	r := NewSchemaRegistry()
	// 自定义覆盖 NodeDeleted 的版本与校验函数
	called := false
	r.Register(EventNodeDeleted, "2.0.0", func(e *Event) error {
		called = true
		return nil
	})

	s, ok := r.GetSchema(EventNodeDeleted)
	require.True(t, ok)
	assert.Equal(t, "2.0.0", s.Version)

	e := validEvent(EventNodeDeleted, NodeDeletedPayload{NodeID: "node-001"})
	require.NoError(t, r.Validate(e))
	assert.True(t, called, "自定义校验函数应被调用")
}

// TestSchemaRegistry_Register_UnknownType 校验注册未知事件类型时字段为空但可注册。
func TestSchemaRegistry_Register_UnknownType(t *testing.T) {
	r := NewSchemaRegistry()
	unknown := EventType("CustomEvent")
	r.Register(unknown, "1.0.0", nil)
	s, ok := r.GetSchema(unknown)
	require.True(t, ok)
	assert.Equal(t, "1.0.0", s.Version)
	assert.Empty(t, s.Fields)
}

// TestSchemaRegistry_FieldsDerived 校验通过反射派生的字段定义正确。
func TestSchemaRegistry_FieldsDerived(t *testing.T) {
	r := NewSchemaRegistry()
	s, ok := r.GetSchema(EventTelemetryReceived)
	require.True(t, ok)
	// TelemetryReceivedPayload 字段：satelliteId/timestamp/parameters/quality 均无 omitempty
	names := make(map[string]FieldDef, len(s.Fields))
	for _, f := range s.Fields {
		names[f.Name] = f
	}
	require.Contains(t, names, "satelliteId")
	assert.Equal(t, "string", names["satelliteId"].Type)
	assert.True(t, names["satelliteId"].Required)

	require.Contains(t, names, "timestamp")
	assert.Equal(t, "timestamp", names["timestamp"].Type)
	assert.True(t, names["timestamp"].Required)

	require.Contains(t, names, "parameters")
	assert.Equal(t, "object", names["parameters"].Type)
	assert.True(t, names["parameters"].Required)

	require.Contains(t, names, "quality")
	assert.Equal(t, "string", names["quality"].Type)
	assert.True(t, names["quality"].Required)
}
