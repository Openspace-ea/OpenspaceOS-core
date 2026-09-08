package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest 描述插件的元数据信息。
//
// 插件通过清单文件声明自身名称、版本、扩展点、权限和依赖等。
// 清单文件支持 plugin.yaml / plugin.yml（YAML）或 manifest.json（JSON）格式。
type Manifest struct {
	// Name 插件名称，全局唯一，必填。
	Name string `json:"name" yaml:"name"`
	// Version 语义化版本号，必填。
	Version string `json:"version" yaml:"version"`
	// Description 插件描述。
	Description string `json:"description" yaml:"description"`
	// Author 插件作者。
	Author string `json:"author" yaml:"author"`
	// ExtensionPoints 插件实现的扩展点列表，如 ["TelemetryParser", "CommandAdapter"]。
	ExtensionPoints []string `json:"extensionPoints" yaml:"extensionPoints"`
	// Permissions 插件申请的权限列表，如 ["event.publish", "kg.query"]。
	Permissions []string `json:"permissions" yaml:"permissions"`
	// Dependencies 插件依赖及其版本约束，如 {"openspace-os-core": ">=1.0.0"}。
	Dependencies map[string]string `json:"dependencies" yaml:"dependencies"`
	// EntryPoint 插件二进制路径（MVP 阶段仅作记录，实际使用内置注册）。
	EntryPoint string `json:"entryPoint" yaml:"entryPoint"`
}

// LoadManifest 从指定路径加载插件清单文件。
// 根据文件扩展名自动选择 YAML 或 JSON 解析器。
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取清单文件失败: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	m := &Manifest{}
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, m); err != nil {
			return nil, fmt.Errorf("解析 YAML 清单失败: %w", err)
		}
	case ".json":
		if err := json.Unmarshal(data, m); err != nil {
			return nil, fmt.Errorf("解析 JSON 清单失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的清单文件格式: %s", ext)
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Validate 校验清单的必填字段。
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("清单 name 不能为空")
	}
	if m.Version == "" {
		return fmt.Errorf("清单 version 不能为空")
	}
	if m.EntryPoint == "" {
		return fmt.Errorf("清单 entryPoint 不能为空")
	}
	return nil
}
