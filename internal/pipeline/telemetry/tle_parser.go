package telemetry

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/pkg/model"
)

// TLE 解析相关常量。
const (
	// tleLineLength 标准 TLE 每行长度（含校验位）。
	tleLineLength = 69

	// earthRadiusKm 地球平均半径（km）。
	earthRadiusKm = 6371.0

	// earthGM 地球引力常数 GM（km³/s²）。
	earthGM = 398600.4418

	// minutesPerDay 一天的分钟数。
	minutesPerDay = 1440.0
)

// TLEParser 是内置的两行轨道根数（TLE）解析器。
//
// 输入为标准 TLE 两行文本（可选前置名称行），例如：
//
//	ISS (ZARYA)
//	1 25544U 98067A   08264.51782528 -.00002182  00000-0 -11606-4 0  2927
//	2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.72125391563537
//
// 解析后输出 TelemetryFrame，Parameters 中 "orbit" 字段包含完整的 OrbitElements。
type TLEParser struct{}

// Name 返回解析器名称。
func (p *TLEParser) Name() string {
	return "tle"
}

// Parse 将 TLE 原始文本解析为遥测帧。
//
// 支持以下输入形式：
//   - 仅两行（Line1 + Line2）
//   - 三行（名称行 + Line1 + Line2）
//   - 多组 TLE（每组之间以空行分隔）
func (p *TLEParser) Parse(raw []byte) ([]plugin.TelemetryFrame, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, fmt.Errorf("TLE 输入数据为空")
	}

	// 按空行分组，每组为一个独立 TLE
	groups := splitTLEGroups(text)
	frames := make([]plugin.TelemetryFrame, 0, len(groups))

	for i, group := range groups {
		line1, line2, err := extractTLELines(group)
		if err != nil {
			return nil, fmt.Errorf("第 %d 组 TLE 解析失败: %w", i+1, err)
		}
		frame, err := parseTLEPair(line1, line2)
		if err != nil {
			return nil, fmt.Errorf("第 %d 组 TLE 解析失败: %w", i+1, err)
		}
		frames = append(frames, *frame)
	}

	if len(frames) == 0 {
		return nil, fmt.Errorf("未解析到任何 TLE 帧")
	}
	return frames, nil
}

// splitTLEGroups 按空行将输入切分为多个 TLE 组。
func splitTLEGroups(text string) [][]string {
	lines := strings.Split(text, "\n")
	var groups [][]string
	var current []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(current) > 0 {
				groups = append(groups, current)
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

// extractTLELines 从一组行中提取出 Line1 与 Line2。
//
// 支持两行（无名称）或三行（含名称）格式。
func extractTLELines(group []string) (line1, line2 string, err error) {
	if len(group) < 2 {
		return "", "", fmt.Errorf("TLE 行数不足，至少需要 2 行，实际 %d 行", len(group))
	}

	// 找到以 "1 " 开头的行作为 Line1
	idx1 := -1
	for i, l := range group {
		if strings.HasPrefix(l, "1 ") {
			idx1 = i
			break
		}
	}
	if idx1 < 0 {
		return "", "", fmt.Errorf("未找到以 '1 ' 开头的 TLE 第一行")
	}
	line1 = group[idx1]

	// Line2 紧跟 Line1 之后
	if idx1+1 >= len(group) {
		return "", "", fmt.Errorf("缺少 TLE 第二行")
	}
	line2 = group[idx1+1]
	if !strings.HasPrefix(line2, "2 ") {
		return "", "", fmt.Errorf("TLE 第二行应以 '2 ' 开头")
	}
	return line1, line2, nil
}

// parseTLEPair 解析一对 TLE 行（Line1 + Line2）为 TelemetryFrame。
func parseTLEPair(line1, line2 string) (*plugin.TelemetryFrame, error) {
	// 格式校验
	if err := validateTLELine(line1, '1'); err != nil {
		return nil, fmt.Errorf("第一行格式错误: %w", err)
	}
	if err := validateTLELine(line2, '2'); err != nil {
		return nil, fmt.Errorf("第二行格式错误: %w", err)
	}

	// checksum 校验
	if err := verifyChecksum(line1); err != nil {
		return nil, fmt.Errorf("第一行校验和失败: %w", err)
	}
	if err := verifyChecksum(line2); err != nil {
		return nil, fmt.Errorf("第二行校验和失败: %w", err)
	}

	// 提取公共字段：NORAD ID（两行应一致）
	noradID1 := strings.TrimSpace(line1[2:7])
	noradID2 := strings.TrimSpace(line2[2:7])
	if noradID1 != noradID2 {
		return nil, fmt.Errorf("两行 NORAD ID 不一致: %s vs %s", noradID1, noradID2)
	}

	// 解析 Line1 的历元
	epochYear := line1[18:20]   // 列 19-20
	epochDayStr := line1[20:32] // 列 21-32
	epoch, err := parseEpoch(epochYear, epochDayStr)
	if err != nil {
		return nil, fmt.Errorf("解析历元失败: %w", err)
	}

	// 提取 Line2 轨道根数
	inclination, err := strconv.ParseFloat(strings.TrimSpace(line2[8:16]), 64) // 列 9-16
	if err != nil {
		return nil, fmt.Errorf("解析倾角失败: %w", err)
	}

	raan, err := strconv.ParseFloat(strings.TrimSpace(line2[17:25]), 64) // 列 18-25
	if err != nil {
		return nil, fmt.Errorf("解析升交点赤经失败: %w", err)
	}

	// 偏心率：列 27-33，前补 "0."
	eccStr := "0." + strings.TrimSpace(line2[26:33])
	eccentricity, err := strconv.ParseFloat(eccStr, 64)
	if err != nil {
		return nil, fmt.Errorf("解析偏心率失败: %w", err)
	}

	argPerigee, err := strconv.ParseFloat(strings.TrimSpace(line2[34:42]), 64) // 列 35-42
	if err != nil {
		return nil, fmt.Errorf("解析近地点幅角失败: %w", err)
	}

	meanAnomaly, err := strconv.ParseFloat(strings.TrimSpace(line2[43:51]), 64) // 列 44-51
	if err != nil {
		return nil, fmt.Errorf("解析平近点角失败: %w", err)
	}

	meanMotion, err := strconv.ParseFloat(strings.TrimSpace(line2[52:63]), 64) // 列 53-63
	if err != nil {
		return nil, fmt.Errorf("解析平均运动失败: %w", err)
	}
	if meanMotion <= 0 {
		return nil, fmt.Errorf("平均运动必须大于 0，实际 %f", meanMotion)
	}

	// 计算派生轨道参数
	period := minutesPerDay / meanMotion                // 周期（分钟）
	semiMajorAxis := computeSemiMajorAxis(period)       // 半长轴（km）
	perigeeAlt := semiMajorAxis*(1-eccentricity) - earthRadiusKm
	apogeeAlt := semiMajorAxis*(1+eccentricity) - earthRadiusKm

	orbit := model.OrbitElements{
		NoradID:        noradID1,
		Inclination:    inclination,
		RAAN:           raan,
		Eccentricity:   eccentricity,
		ArgPerigee:     argPerigee,
		MeanAnomaly:    meanAnomaly,
		MeanMotion:     meanMotion,
		Period:         period,
		PerigeeAltitude: perigeeAlt,
		ApogeeAltitude:  apogeeAlt,
		SemiMajorAxis:   semiMajorAxis,
		Epoch:           epoch,
	}

	frame := &plugin.TelemetryFrame{
		SatelliteID: noradID1,
		Timestamp:   epoch,
		Quality:     "good",
		Parameters: map[string]any{
			"orbit":   orbit,
			"noradId": noradID1,
		},
	}
	return frame, nil
}

// validateTLELine 校验单行 TLE 的基本格式（行号前缀与长度）。
func validateTLELine(line string, expectedPrefix byte) error {
	if len(line) != tleLineLength {
		return fmt.Errorf("行长度应为 %d，实际 %d", tleLineLength, len(line))
	}
	if line[0] != expectedPrefix {
		return fmt.Errorf("行应以 '%c ' 开头，实际首字符 '%c'", expectedPrefix, line[0])
	}
	if line[1] != ' ' {
		return fmt.Errorf("行首第二个字符应为空格")
	}
	return nil
}

// verifyChecksum 校验 TLE 行的校验位。
//
// 校验规则：除最后一位外，每个数字按其值累加，'-' 计为 1，
// 其余字符计为 0；总和 mod 10 应等于最后一位数字。
func verifyChecksum(line string) error {
	if len(line) != tleLineLength {
		return fmt.Errorf("行长度不正确")
	}
	var sum int
	for i := 0; i < tleLineLength-1; i++ {
		c := line[i]
		switch {
		case c >= '0' && c <= '9':
			sum += int(c - '0')
		case c == '-':
			sum += 1
		default:
			// 空格、字母、小数点等不计入
		}
	}
	expected := sum % 10
	actual := int(line[tleLineLength-1] - '0')
	if actual != expected {
		return fmt.Errorf("校验和不匹配: 计算 %d, 实际 %d", expected, actual)
	}
	return nil
}

// parseEpoch 将两位年份与年积日（含小数）解析为 time.Time。
//
// 年份规则：>=57 视为 19xx，否则 20xx。
func parseEpoch(yearStr, dayStr string) (time.Time, error) {
	year2, err := strconv.Atoi(strings.TrimSpace(yearStr))
	if err != nil {
		return time.Time{}, fmt.Errorf("无效的年份 %q: %w", yearStr, err)
	}
	var fullYear int
	if year2 >= 57 {
		fullYear = 1900 + year2
	} else {
		fullYear = 2000 + year2
	}

	dayOfYear, err := strconv.ParseFloat(strings.TrimSpace(dayStr), 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("无效的年积日 %q: %w", dayStr, err)
	}
	if dayOfYear < 1 {
		return time.Time{}, fmt.Errorf("年积日必须 >= 1，实际 %f", dayOfYear)
	}

	// 以当年 1 月 1 日 00:00 UTC 为基准，偏移 (dayOfYear - 1) 天
	startOfYear := time.Date(fullYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	offsetDays := dayOfYear - 1
	dur := time.Duration(offsetDays * 24 * float64(time.Hour))
	return startOfYear.Add(dur), nil
}

// computeSemiMajorAxis 根据轨道周期（分钟）计算半长轴（km）。
//
// 公式：a = (GM / (2π/T)²)^(1/3)，其中 T 为周期（秒），GM 为地球引力常数。
func computeSemiMajorAxis(periodMin float64) float64 {
	tSec := periodMin * 60.0                  // 周期（秒）
	n := 2.0 * math.Pi / tSec                 // 平均角速度（rad/s）
	semiMajor := math.Cbrt(earthGM / (n * n)) // 半长轴（km）
	return semiMajor
}
