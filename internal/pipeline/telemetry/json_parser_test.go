package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// TestJSONParserName 验证 JSONParser 名称。
func TestJSONParserName(t *testing.T) {
	p := &JSONParser{}
	assert.Equal(t, "json", p.Name())
}

// TestJSONParserSingleLine 测试单行 JSON 解析为 TelemetryFrame。
func TestJSONParserSingleLine(t *testing.T) {
	p := &JSONParser{}
	raw := []byte(`{"satelliteId":"sat-1","timestamp":"2026-07-01T00:00:00Z","parameters":{"temp":45.2},"quality":"good"}`)

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1)

	frame := frames[0]
	assert.Equal(t, "sat-1", frame.SatelliteID)
	assert.Equal(t, "good", frame.Quality)
	assert.Equal(t, "2026-07-01T00:00:00Z", frame.Timestamp.Format(time.RFC3339))

	temp, ok := frame.Parameters["temp"]
	require.True(t, ok)
	assert.InDelta(t, 45.2, temp, 0.001)
}

// TestJSONParserMultiLine 测试多行 JSON 解析。
func TestJSONParserMultiLine(t *testing.T) {
	p := &JSONParser{}
	raw := []byte(`{"satelliteId":"sat-1","timestamp":"2026-07-01T00:00:00Z","parameters":{"a":1},"quality":"good"}` + "\n" +
		`{"satelliteId":"sat-2","timestamp":"2026-07-01T00:01:00Z","parameters":{"b":2},"quality":"bad"}` + "\n")

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 2)
	assert.Equal(t, "sat-1", frames[0].SatelliteID)
	assert.Equal(t, "sat-2", frames[1].SatelliteID)
	assert.Equal(t, "bad", frames[1].Quality)
}

// TestJSONParserMissingTimestamp 测试缺失时间戳时使用当前时间填充。
func TestJSONParserMissingTimestamp(t *testing.T) {
	p := &JSONParser{}
	raw := []byte(`{"satelliteId":"sat-1","parameters":{"temp":45.2},"quality":"good"}`)

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.False(t, frames[0].Timestamp.IsZero())
}

// TestJSONParserMissingSatelliteID 测试缺少 satelliteId 返回错误。
func TestJSONParserMissingSatelliteID(t *testing.T) {
	p := &JSONParser{}
	raw := []byte(`{"timestamp":"2026-07-01T00:00:00Z","parameters":{},"quality":"good"}`)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "satelliteId")
}

// TestJSONParserInvalidJSON 测试无效 JSON 返回错误。
func TestJSONParserInvalidJSON(t *testing.T) {
	p := &JSONParser{}
	raw := []byte(`{"satelliteId":"sat-1", invalid json}`)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "JSON 解析失败")
}

// TestJSONParserEmptyInput 测试空输入返回错误。
func TestJSONParserEmptyInput(t *testing.T) {
	p := &JSONParser{}
	_, err := p.Parse([]byte(""))
	require.Error(t, err)

	_, err = p.Parse([]byte("   \n  \n"))
	require.Error(t, err)
}

// TestJSONParserImplementsInterface 编译期断言 JSONParser 实现 TelemetryParser 接口。
func TestJSONParserImplementsInterface(t *testing.T) {
	var _ plugin.TelemetryParser = (*JSONParser)(nil)
}
