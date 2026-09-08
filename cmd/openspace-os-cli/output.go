// Package main 提供 Openspace OS CLI 工具的输出格式化能力。
//
// 支持 JSON 与 table 两种输出格式，由全局 --output 参数控制。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PrintJSON 以缩进格式将 v 序列化为 JSON 并输出到标准输出。
func PrintJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON 序列化失败: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// PrintTable 以对齐的表格形式输出到标准输出。
// headers 为列标题，rows 为数据行（每行单元格数量应与 headers 一致）。
func PrintTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = displayWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				w := displayWidth(cell)
				if w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	// 表头
	printTableRow(headers, widths)
	// 分隔线
	separators := make([]string, len(headers))
	for i, w := range widths {
		separators[i] = strings.Repeat("-", w)
	}
	printTableRow(separators, widths)
	// 数据行
	for _, row := range rows {
		printTableRow(row, widths)
	}
}

// printTableRow 按列宽对齐打印一行。
func printTableRow(cells []string, widths []int) {
	parts := make([]string, len(widths))
	for i := 0; i < len(widths); i++ {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		parts[i] = cell + strings.Repeat(" ", widths[i]-displayWidth(cell))
	}
	fmt.Println(strings.Join(parts, "  "))
}

// displayWidth 返回字符串的显示宽度。
// 当前按 rune 数计算，适用于 ASCII 与中文混排的基本场景。
func displayWidth(s string) int {
	return len([]rune(s))
}

// PrintError 将错误信息输出到标准错误。
func PrintError(err error) {
	fmt.Fprintf(os.Stderr, "错误: %v\n", err)
}

// isTableOutput 判断当前输出格式是否为 table。
func isTableOutput() bool {
	return outputFmt == "table"
}

// printAny 根据输出格式输出单个对象。
// table 模式下使用给定的 headers/extractor 渲染表格，否则输出 JSON。
func printAny(v any, headers []string, row []string) {
	if isTableOutput() {
		PrintTable(headers, [][]string{row})
		return
	}
	PrintJSON(v)
}

// printList 根据输出格式输出列表。
// table 模式下使用给定的 headers/rows 渲染表格，否则输出 JSON。
func printList(v any, headers []string, rows [][]string) {
	if isTableOutput() {
		PrintTable(headers, rows)
		return
	}
	PrintJSON(v)
}
