package telemetry

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// JSONParser 是内置的 JSON 行格式遥测解析器。
//
// 解析每行一个 JSON 对象的遥测数据：
//
//	{"satelliteId":"sat-1","timestamp":"2026-07-01T00:00:00Z","parameters":{"temp":45.2},"quality":"good"}
//
// 支持多行输入，每行解析为一个 TelemetryFrame。
// 若 timestamp 字段缺失或为零值，使用当前时间填充。
type JSONParser struct{}

// Name 返回解析器名称。
func (p *JSONParser) Name() string {
	return "json"
}

// Parse 将原始数据解析为遥测帧列表。
//
// 输入可以是单行或多行 JSON，每行一个独立 JSON 对象。
// 空行会被跳过。
func (p *JSONParser) Parse(raw []byte) ([]plugin.TelemetryFrame, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("输入数据为空")
	}

	// 按行切分
	lines := strings.Split(text, "\n")
	frames := make([]plugin.TelemetryFrame, 0, len(lines))

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame plugin.TelemetryFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return nil, fmt.Errorf("第 %d 行 JSON 解析失败: %w", i+1, err)
		}
		if frame.SatelliteID == "" {
			return nil, fmt.Errorf("第 %d 行缺少 satelliteId", i+1)
		}
		// 缺失时间戳时使用当前时间
		if frame.Timestamp.IsZero() {
			frame.Timestamp = time.Now()
		}
		frames = append(frames, frame)
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("未解析到任何遥测帧")
	}
	return frames, nil
}
