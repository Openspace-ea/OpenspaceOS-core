package event

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// SchemaVersionDefault 默认语义化版本号。
const SchemaVersionDefault = "1.0.0"

// FieldDef 描述事件 Payload 中的一个字段定义。
type FieldDef struct {
	// Name 字段名（对应 JSON tag）。
	Name string `json:"name"`
	// Type 字段类型（string/number/boolean/timestamp/object/array）。
	Type string `json:"type"`
	// Required 是否必填。
	Required bool `json:"required"`
}

// Schema 描述一个事件类型的 Schema 信息。
type Schema struct {
	// EventType 事件类型。
	EventType EventType `json:"eventType"`
	// Version 语义化版本号。
	Version string `json:"version"`
	// Fields Payload 字段定义列表。
	Fields []FieldDef `json:"fields"`
}

// Validator 是事件自定义校验函数。
type Validator func(*Event) error

// SchemaRegistry 是事件 Schema 注册中心，
// 管理事件类型、版本号、字段定义与校验函数。
type SchemaRegistry struct {
	mu         sync.RWMutex
	schemas    map[EventType]Schema
	validators map[EventType]Validator
}

// NewSchemaRegistry 创建 Schema 注册中心，并自动注册 14 个默认事件 Schema。
func NewSchemaRegistry() *SchemaRegistry {
	r := &SchemaRegistry{
		schemas:    make(map[EventType]Schema),
		validators: make(map[EventType]Validator),
	}
	r.registerDefaults()
	return r
}

// Register 注册或覆盖一个事件类型的 Schema。
//
// 若该事件类型存在已知的默认 Payload 结构体，则自动派生字段定义；
// 否则字段定义为空。validator 可为 nil，表示使用默认字段校验。
func (r *SchemaRegistry) Register(eventType EventType, version string, validator Validator) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fields := []FieldDef{}
	if payload, ok := defaultPayloads[eventType]; ok {
		fields = extractFields(payload)
	}
	r.schemas[eventType] = Schema{
		EventType: eventType,
		Version:   version,
		Fields:    fields,
	}
	r.validators[eventType] = validator
}

// GetSchema 返回指定事件类型的 Schema 信息。
func (r *SchemaRegistry) GetSchema(eventType EventType) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[eventType]
	return s, ok
}

// ListSchemas 返回所有已注册的 Schema，按事件类型名称排序。
func (r *SchemaRegistry) ListSchemas() []Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Schema, 0, len(r.schemas))
	for _, s := range r.schemas {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return string(out[i].EventType) < string(out[j].EventType)
	})
	return out
}

// Validate 校验事件是否符合注册的 Schema。
//
// 校验内容包括：事件头必填字段、事件类型是否已注册、
// Payload 必填字段是否存在且非空、自定义校验函数。
func (r *SchemaRegistry) Validate(e *Event) error {
	if e == nil {
		return errors.New("事件不能为空")
	}
	if e.EventID == "" {
		return errors.New("eventId 不能为空")
	}
	if e.EventType == "" {
		return errors.New("eventType 不能为空")
	}
	if e.SourceNodeID == "" {
		return errors.New("sourceNodeId 不能为空")
	}
	if e.Timestamp.IsZero() {
		return errors.New("timestamp 不能为零值")
	}

	r.mu.RLock()
	schema, schemaOk := r.schemas[e.EventType]
	validator := r.validators[e.EventType]
	r.mu.RUnlock()

	if !schemaOk {
		return fmt.Errorf("未注册的事件类型: %s", e.EventType)
	}
	if e.Payload == nil {
		return fmt.Errorf("事件 %s 的 payload 不能为空", e.EventType)
	}

	// 校验必填字段存在且非空
	for _, field := range schema.Fields {
		if !field.Required {
			continue
		}
		val, exists := e.Payload[field.Name]
		if !exists || val == nil {
			return fmt.Errorf("事件 %s 缺少必填字段: %s", e.EventType, field.Name)
		}
		if isEmptyValue(val) {
			return fmt.Errorf("事件 %s 必填字段为空: %s", e.EventType, field.Name)
		}
	}

	if validator != nil {
		return validator(e)
	}
	return nil
}

// registerDefaults 自动注册 14 个事件类型的默认 Schema。
func (r *SchemaRegistry) registerDefaults() {
	for eventType := range defaultPayloads {
		r.Register(eventType, SchemaVersionDefault, nil)
	}
}

// defaultPayloads 维护事件类型与默认 Payload 结构体的映射，
// 用于通过反射派生字段定义。
var defaultPayloads = map[EventType]any{
	EventTelemetryReceived:      TelemetryReceivedPayload{},
	EventStateUpdated:           StateUpdatedPayload{},
	EventNodeRegistered:         NodeRegisteredPayload{},
	EventNodeUpdated:            NodeUpdatedPayload{},
	EventNodeDeleted:            NodeDeletedPayload{},
	EventRelationshipCreated:    RelationshipCreatedPayload{},
	EventRelationshipDeleted:    RelationshipDeletedPayload{},
	EventTaskStatusChanged:      TaskStatusChangedPayload{},
	EventTaskScheduled:          TaskScheduledPayload{},
	EventHealthAlarm:            HealthAlarmPayload{},
	EventCommandSent:            CommandSentPayload{},
	EventCommandAcked:           CommandAckedPayload{},
	EventConjunctionAlert:       ConjunctionAlertPayload{},
	EventResourceMatchCompleted: ResourceMatchCompletedPayload{},
}

// extractFields 通过反射从 Payload 结构体派生字段定义。
//
// 字段名取自 json tag；类型映射到 JSON 语义类型；
// 含 omitempty 标记的字段视为非必填，否则为必填。
func extractFields(payload any) []FieldDef {
	t := reflect.TypeOf(payload)
	if t == nil {
		return nil
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var fields []FieldDef
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		// 跳过非导出字段
		if !f.IsExported() {
			continue
		}
		jsonTag := f.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		parts := strings.Split(jsonTag, ",")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		omitempty := false
		for _, p := range parts[1:] {
			if strings.TrimSpace(p) == "omitempty" {
				omitempty = true
				break
			}
		}
		fields = append(fields, FieldDef{
			Name:     name,
			Type:     jsonType(f.Type),
			Required: !omitempty,
		})
	}
	return fields
}

// jsonType 将 Go 类型映射为 JSON 语义类型字符串。
func jsonType(t reflect.Type) string {
	if t == reflect.TypeOf(time.Time{}) {
		return "timestamp"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Map, reflect.Struct:
		return "object"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "any"
	}
}

// isEmptyValue 判断 Payload 中的值是否视为空。
//
// 字符串空值视为空；nil 视为空；其他类型不视为空。
func isEmptyValue(val any) bool {
	if val == nil {
		return true
	}
	if s, ok := val.(string); ok && s == "" {
		return true
	}
	return false
}
