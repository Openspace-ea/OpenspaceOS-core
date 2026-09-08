package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// ISS 真实 TLE 数据（2008 年历元）。
const (
	issTLELine1 = "1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927"
	issTLELine2 = "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537"
	issTLEName  = "ISS (ZARYA)"
)

// TestTLEParserName 验证 TLEParser 名称。
func TestTLEParserName(t *testing.T) {
	p := &TLEParser{}
	assert.Equal(t, "tle", p.Name())
}

// TestTLEParserISSRealData 使用 ISS 真实 TLE 验证解析结果。
func TestTLEParserISSRealData(t *testing.T) {
	p := &TLEParser{}
	raw := []byte(issTLELine1 + "\n" + issTLELine2)

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1)

	frame := frames[0]
	assert.Equal(t, "25544", frame.SatelliteID)
	assert.Equal(t, "good", frame.Quality)

	// 验证 orbit 字段存在
	orbitAny, ok := frame.Parameters["orbit"]
	require.True(t, ok, "应包含 orbit 字段")
	orbit, ok := orbitAny.(model.OrbitElements)
	require.True(t, ok, "orbit 应为 model.OrbitElements 类型")

	// 验证关键字段
	assert.Equal(t, "25544", orbit.NoradID)
	assert.InDelta(t, 51.6416, orbit.Inclination, 0.0001)
	assert.InDelta(t, 247.4627, orbit.RAAN, 0.0001)
	assert.InDelta(t, 0.0006703, orbit.Eccentricity, 0.0000001)
	assert.InDelta(t, 130.5360, orbit.ArgPerigee, 0.0001)
	assert.InDelta(t, 325.0288, orbit.MeanAnomaly, 0.0001)
	assert.InDelta(t, 15.72125391, orbit.MeanMotion, 0.0000001)

	// 验证派生参数
	assert.InDelta(t, 1440.0/15.72125391, orbit.Period, 0.01)
	assert.Greater(t, orbit.SemiMajorAxis, 6700.0) // 约 6730 km
	assert.Less(t, orbit.SemiMajorAxis, 6800.0)
	assert.Greater(t, orbit.PerigeeAltitude, 300.0) // 约 350 km
	assert.Less(t, orbit.PerigeeAltitude, 400.0)
	assert.Greater(t, orbit.ApogeeAltitude, 300.0)
	assert.Less(t, orbit.ApogeeAltitude, 400.0)

	// 验证历元：2008 年第 264 天 ≈ 2008-09-20
	assert.Equal(t, 2008, orbit.Epoch.Year())
	assert.Equal(t, time.September, orbit.Epoch.Month())
	assert.Equal(t, 20, orbit.Epoch.Day())
}

// TestTLEParserThreeLine 测试三行格式（含名称行）。
func TestTLEParserThreeLine(t *testing.T) {
	p := &TLEParser{}
	raw := []byte(issTLEName + "\n" + issTLELine1 + "\n" + issTLELine2)

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, "25544", frames[0].SatelliteID)
}

// TestTLEParserWithTrailingWhitespace 测试带尾部空白与空行的输入。
func TestTLEParserWithTrailingWhitespace(t *testing.T) {
	p := &TLEParser{}
	raw := []byte("\n" + issTLELine1 + "\n" + issTLELine2 + "\n\n")

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 1)
}

// TestTLEParserMultipleGroups 测试多组 TLE 输入。
func TestTLEParserMultipleGroups(t *testing.T) {
	p := &TLEParser{}
	raw := []byte(issTLELine1 + "\n" + issTLELine2 + "\n\n" +
		"1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927\n" +
		"2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537\n")

	frames, err := p.Parse(raw)
	require.NoError(t, err)
	require.Len(t, frames, 2)
	assert.Equal(t, "25544", frames[0].SatelliteID)
	assert.Equal(t, "25544", frames[1].SatelliteID)
}

// TestTLEParserWrongChecksum 测试错误的校验和返回错误。
func TestTLEParserWrongChecksum(t *testing.T) {
	p := &TLEParser{}
	// 将第一行校验位 '7' 改为 '0'
	badLine1 := issTLELine1[:len(issTLELine1)-1] + "0"
	raw := []byte(badLine1 + "\n" + issTLELine2)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "校验和")
}

// TestTLEParserWrongChecksumLine2 测试第二行校验和错误。
func TestTLEParserWrongChecksumLine2(t *testing.T) {
	p := &TLEParser{}
	// 将第二行校验位 '7' 改为 '1'
	badLine2 := issTLELine2[:len(issTLELine2)-1] + "1"
	raw := []byte(issTLELine1 + "\n" + badLine2)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "校验和")
}

// TestTLEParserWrongLength 测试行长度不正确返回错误。
func TestTLEParserWrongLength(t *testing.T) {
	p := &TLEParser{}
	// 截断第一行
	shortLine1 := issTLELine1[:60]
	raw := []byte(shortLine1 + "\n" + issTLELine2)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "长度")
}

// TestTLEParserWrongPrefix 测试行前缀错误返回错误。
func TestTLEParserWrongPrefix(t *testing.T) {
	p := &TLEParser{}
	// 第一行不以 "1 " 开头
	badLine1 := "X" + issTLELine1[1:]
	raw := []byte(badLine1 + "\n" + issTLELine2)

	_, err := p.Parse(raw)
	require.Error(t, err)
}

// TestTLEParserMissingLine2 测试缺少第二行返回错误。
func TestTLEParserMissingLine2(t *testing.T) {
	p := &TLEParser{}
	raw := []byte(issTLELine1)

	_, err := p.Parse(raw)
	require.Error(t, err)
}

// TestTLEParserMismatchedNoradID 测试两行 NORAD ID 不一致返回错误。
func TestTLEParserMismatchedNoradID(t *testing.T) {
	p := &TLEParser{}
	// 第二行使用不同的 NORAD ID（25545），并重算校验位（原 7 → 8）
	badLine2 := "2 25545  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563538"
	raw := []byte(issTLELine1 + "\n" + badLine2)

	_, err := p.Parse(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NORAD ID")
}

// TestTLEParserEmptyInput 测试空输入返回错误。
func TestTLEParserEmptyInput(t *testing.T) {
	p := &TLEParser{}
	_, err := p.Parse([]byte(""))
	require.Error(t, err)
}

// TestTLEParserImplementsInterface 编译期断言 TLEParser 实现 TelemetryParser 接口。
func TestTLEParserImplementsInterface(t *testing.T) {
	var _ plugin.TelemetryParser = (*TLEParser)(nil)
}
