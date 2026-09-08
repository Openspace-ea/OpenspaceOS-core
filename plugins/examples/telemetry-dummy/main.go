// Package telemetrydummy 是一个示例遥测解析插件。
//
// 通过 init() 自动注册到全局插件注册表，
// main.go 中通过 blank import 即可启用：
//
//	import _ "github.com/openspace-os/openspace-os-core/plugins/examples/telemetry-dummy"
//
// 解析 JSON 行格式遥测数据，每行一个 JSON 对象：
//
//	{"satelliteId":"sat-1","timestamp":"2026-07-01T00:00:00Z","parameters":{"temp":45.2},"quality":"good"}
package telemetrydummy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// DummyParser 是示例遥测解析器，解析 JSON 行格式遥测数据。
type DummyParser struct{}

// Name 返回解析器名称。
func (p *DummyParser) Name() string {
	return "telemetry-dummy"
}

// Parse 解析 JSON 行格式遥测数据。
//
// 支持多行输入，每行一个 JSON 对象。
// 若 timestamp 字段缺失或为零值，使用当前时间填充。
func (p *DummyParser) Parse(raw []byte) ([]plugin.TelemetryFrame, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	frames := make([]plugin.TelemetryFrame, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame plugin.TelemetryFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return nil, fmt.Errorf("第 %d 行解析失败: %w", i+1, err)
		}
		if frame.Timestamp.IsZero() {
			frame.Timestamp = time.Now()
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

// init 将示例解析器注册到全局插件注册表。
func init() {
	plugin.RegisterParser("telemetry-dummy", &DummyParser{})
}
