// Package config 负责 Openspace OS Core 的配置加载与管理。
//
// 通过 viper 支持从 YAML 配置文件读取配置，并合并环境变量覆盖。
// 环境变量使用 Openspace OS_ 前缀，层级用下划线分隔，例如 OPENSPACE_SERVER_HTTP_PORT。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 是 Openspace OS Core 的顶层配置结构。
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	Data   DataConfig   `mapstructure:"data"`
	Bus    BusConfig    `mapstructure:"bus"`
	Plugin PluginConfig `mapstructure:"plugin"`
	Log    LogConfig    `mapstructure:"log"`
	Otel   OtelConfig   `mapstructure:"otel"`
	Auth   AuthConfig   `mapstructure:"auth"`
}

// BusConfig 定义事件总线配置。
type BusConfig struct {
	// Type 事件总线类型："local"（默认，进程内）或 "nats"（NATS JetStream）。
	Type string `mapstructure:"type"`
	// URL NATS 连接地址（type=nats 时使用），如 nats://localhost:4222。
	URL string `mapstructure:"url"`
	// StreamName JetStream stream 名称，默认 "events"。
	StreamName string `mapstructure:"stream_name"`
	// MaxStreamAge stream 事件保留时长，默认 168h（7 天）。
	MaxStreamAge time.Duration `mapstructure:"max_stream_age"`
}

// ServerConfig 定义各服务端口配置。
type ServerConfig struct {
	HTTPPort      int `mapstructure:"http_port"`
	GRPCPort      int `mapstructure:"grpc_port"`
	TelemetryPort int `mapstructure:"telemetry_port"`
}

// DataConfig 定义数据存储配置。
type DataConfig struct {
	// Type 存储类型："sqlite"（默认）或 "postgres"。
	Type string `mapstructure:"type"`
	// SQLitePath SQLite 数据库文件路径（type=sqlite 时使用）。
	SQLitePath string `mapstructure:"sqlite_path"`
	// PostgresDSN PostgreSQL 连接串（type=postgres 时使用）。
	PostgresDSN string `mapstructure:"postgres_dsn"`
}

// PluginConfig 定义插件配置。
type PluginConfig struct {
	Directory string `mapstructure:"directory"`
}

// LogConfig 定义日志配置。
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// OtelConfig 定义 OpenTelemetry 配置。
type OtelConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Endpoint    string `mapstructure:"endpoint"`
	ServiceName string `mapstructure:"service_name"`
}

// AuthConfig 定义认证与授权配置。
//
// MVP 阶段支持通过 enabled 开关关闭认证以便测试与演示，
// 但代码中需实现完整的认证逻辑。
type AuthConfig struct {
	// Enabled 是否启用认证。关闭时所有路由无需认证。
	Enabled bool `mapstructure:"enabled"`
	// SecretKey JWT 签名密钥。生产环境务必通过环境变量或配置文件覆盖。
	SecretKey string `mapstructure:"secret_key"`
	// TokenDuration Token 有效期（小时），默认 24。
	TokenDuration int `mapstructure:"token_duration"`
}

// Load 从指定路径加载配置文件，并合并环境变量与默认值。
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	// 环境变量前缀 OPENSPACE_，层级用下划线分隔
	v.SetEnvPrefix("OPENSPACE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 设置默认值
	v.SetDefault("server.http_port", 8080)
	v.SetDefault("server.grpc_port", 9090)
	v.SetDefault("server.telemetry_port", 7000)
	v.SetDefault("data.type", "sqlite")
	v.SetDefault("data.sqlite_path", "./data/openspace-os.db")
	v.SetDefault("data.postgres_dsn", "")
	v.SetDefault("bus.type", "local")
	v.SetDefault("bus.url", "nats://localhost:4222")
	v.SetDefault("bus.stream_name", "events")
	v.SetDefault("bus.max_stream_age", 168*time.Hour)
	v.SetDefault("plugin.directory", "./plugins")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("otel.enabled", false)
	v.SetDefault("otel.endpoint", "")
	v.SetDefault("otel.service_name", "openspace-os-core")

	// 认证默认关闭（MVP 阶段便于测试）；secret_key 提供开发用默认值
	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.secret_key", "openspace-os-core-dev-secret-key")
	v.SetDefault("auth.token_duration", 24)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	return &cfg, nil
}
