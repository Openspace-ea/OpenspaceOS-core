// Package main 是 Openspace OS Core 服务的入口。
//
// 使用 cobra 命令结构组织，通过 run 子命令启动 HTTP 服务，
// 暴露 REST API（/api/v1）与健康检查端点，并支持优雅关闭。
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/openspace-os/openspace-os-core/internal/api/grpc"
	"github.com/openspace-os/openspace-os-core/internal/api/rest"
	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/config"
	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/observability"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/command"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
	"github.com/openspace-os/openspace-os-core/internal/storage"
	"github.com/openspace-os/openspace-os-core/internal/usage"

	// 示例内置插件，通过 init() 自动注册到全局插件注册表
	_ "github.com/openspace-os/openspace-os-core/plugins/examples/telemetry-dummy"

	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// 全局配置文件路径与日志实例
var (
	configPath string
	logger     *slog.Logger
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "openspace-os-core",
		Short: "Openspace OS Core 服务",
		Long:  "Openspace OS Core —— 太空操作系统核心平台服务。",
	}

	// run 子命令：启动 Core 服务
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "启动 Openspace OS Core 服务",
		RunE:  runServer,
	}
	runCmd.Flags().StringVarP(&configPath, "config", "c", "config.yaml", "配置文件路径")

	rootCmd.AddCommand(runCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
		os.Exit(1)
	}
}

// runServer 负责加载配置、初始化日志与各组件，并启动 HTTP 服务。
func runServer(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		// 配置加载失败时使用默认 JSON 日志输出错误
		slog.New(slog.NewJSONHandler(os.Stdout, nil)).Error("加载配置失败", "error", err)
		return err
	}

	// 初始化结构化日志（JSON 格式输出到 stdout），并注入 traceId 自动记录能力
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.Log.Level),
	}))
	logger = observability.TraceLogger(logger)
	slog.SetDefault(logger)

	logger.Info("启动 Openspace OS Core 服务",
		"config", configPath,
		"http_port", cfg.Server.HTTPPort,
		"grpc_port", cfg.Server.GRPCPort,
		"telemetry_port", cfg.Server.TelemetryPort,
	)

	// 初始化 OpenTelemetry（tracing + metrics 导出）
	// 失败时降级为 noop 模式，不阻断服务启动
	otelShutdown, err := observability.Init(observability.OtelConfig{
		Enabled:     cfg.Otel.Enabled,
		Endpoint:    cfg.Otel.Endpoint,
		ServiceName: cfg.Otel.ServiceName,
	})
	if err != nil {
		logger.Warn("OTel 初始化失败，降级为 noop 模式", "error", err)
	}
	defer func() {
		if err := otelShutdown(); err != nil {
			logger.Error("OTel shutdown 失败", "error", err)
		}
	}()

	if cfg.Otel.Enabled {
		logger.Info("OpenTelemetry 已启用", "endpoint", cfg.Otel.Endpoint, "service", cfg.Otel.ServiceName)
	} else {
		logger.Info("OpenTelemetry 未启用（otel.enabled=false）")
	}

	// 创建核心 metrics（注册到全局 MeterProvider，OTel 未启用时为 noop）
	coreMetrics, err := observability.NewCoreMetrics()
	if err != nil {
		logger.Warn("创建 CoreMetrics 失败，继续启动", "error", err)
	}
	_ = coreMetrics // 后续可注入到各组件中

	// 确保数据目录存在（SQLite 数据库文件所在目录）
	dbDir := filepath.Dir(cfg.Data.SQLitePath)
	if dbDir != "" && dbDir != "." {
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			logger.Error("创建数据目录失败", "dir", dbDir, "error", err)
			return fmt.Errorf("创建数据目录失败: %w", err)
		}
	}

	ctx := context.Background()
	dbRebind := core.RebindIdentity

	// 初始化数据库：支持 sqlite（默认）与 postgres 两种存储后端
	var db *sql.DB
	if cfg.Data.Type == "postgres" {
		if cfg.Data.PostgresDSN == "" {
			logger.Error("存储类型为 postgres 但未配置 data.postgres_dsn")
			return fmt.Errorf("存储类型为 postgres 但未配置 data.postgres_dsn")
		}
		db, err = core.InitPostgresDB(ctx, cfg.Data.PostgresDSN)
		if err != nil {
			logger.Error("初始化 postgres 数据库失败", "error", err)
			return fmt.Errorf("初始化 postgres 数据库失败: %w", err)
		}
		dbRebind = core.RebindPostgres
		logger.Info("后端存储已启用 PostgreSQL")
	} else {
		db, err = core.InitDB(cfg.Data.SQLitePath)
		if err != nil {
			logger.Error("初始化数据库失败", "error", err)
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
		logger.Info("后端存储已启用 SQLite", "path", cfg.Data.SQLitePath)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			logger.Error("关闭数据库失败", "error", cerr)
		}
	}()

	// 创建 Node 和 Relationship 仓储（按存储后端选择占位符适配）
	var nodeRepo core.NodeRepository
	var relRepo core.RelationshipRepository
	if cfg.Data.Type == "postgres" {
		nodeRepo = core.NewPostgresNodeRepository(db)
		relRepo = core.NewPostgresRelationshipRepository(db)
	} else {
		nodeRepo = core.NewSQLiteNodeRepository(db)
		relRepo = core.NewSQLiteRelationshipRepository(db)
	}

	// 创建事件 Schema 注册中心
	registry := event.NewSchemaRegistry()

	// 创建事件总线：支持 local（进程内，默认）与 nats（NATS JetStream）两种后端。
	// NATS 后端承担事件写入/持久化/分发职责，无需独立 EventStore；
	// local 后端沿用 SQLite EventStore，与 MVP 行为一致。
	var rawBus core.MessageBus
	switch cfg.Bus.Type {
	case "nats":
		var opts []core.NATSBusOption
		if cfg.Bus.StreamName != "" {
			opts = append(opts, core.WithStreamName(cfg.Bus.StreamName))
		}
		if cfg.Bus.MaxStreamAge > 0 {
			opts = append(opts, core.WithMaxStreamAge(cfg.Bus.MaxStreamAge))
		}
		nb, err := core.NewNATSBus(cfg.Bus.URL, registry, logger, opts...)
		if err != nil {
			logger.Error("初始化 NATS 事件总线失败", "error", err)
			return fmt.Errorf("初始化 NATS 事件总线失败: %w", err)
		}
		rawBus = nb
		logger.Info("事件总线已启用 NATS JetStream", "url", cfg.Bus.URL)
	default:
		store, err := core.NewSQLiteEventStore(ctx, cfg.Data.SQLitePath, core.StoreConfig{})
		if err != nil {
			logger.Error("创建事件存储失败", "error", err)
			return fmt.Errorf("创建事件存储失败: %w", err)
		}
		rawBus = core.NewLocalBus(store, registry, logger)
		logger.Info("事件总线已启用本地进程内（LocalBus）")
	}
	// 使用 TracedBus 包装以添加 trace 埋点
	bus := observability.NewTracedBus(rawBus)

	// 创建 KG 领域服务
	kg := core.NewKGService(nodeRepo, relRepo, bus, logger)
	// 注入数据库连接，使 DeleteNode 在事务中级联删除关系；按存储后端适配占位符
	kg.SetDB(db)
	kg.SetRebind(dbRebind)

	// 初始化认证模块：UserStore 复用主数据库连接（按存储后端选择实现）
	var userStore *auth.SQLiteUserStore
	if cfg.Data.Type == "postgres" {
		userStore = auth.NewPostgresUserStore(db)
	} else {
		userStore = auth.NewSQLiteUserStore(db)
	}
	if err := userStore.InitSchema(ctx); err != nil {
		logger.Error("初始化 users 表失败", "error", err)
		return fmt.Errorf("初始化 users 表失败: %w", err)
	}

	// 创建 TokenManager（secret key 与有效期从配置读取）
	tokenDuration := time.Duration(cfg.Auth.TokenDuration) * time.Hour
	tm := auth.NewTokenManager(cfg.Auth.SecretKey, tokenDuration)

	// 创建认证 Service
	authSvc := auth.NewService(userStore, tm, logger)

	// 初始化机器接入方（Client）存储与 Service
	var clientStore auth.ClientStore
	if cfg.Data.Type == "postgres" {
		clientStore = auth.NewPostgresClientStore(db)
	} else {
		clientStore = auth.NewSQLiteClientStore(db)
	}
	if err := clientStore.InitSchema(ctx); err != nil {
		logger.Error("初始化 clients 表失败", "error", err)
		return fmt.Errorf("初始化 clients 表失败: %w", err)
	}
	clientSvc := auth.NewClientService(clientStore, tm, logger)

	// 初始化默认 admin 用户（用户表为空时创建）
	if err := authSvc.EnsureDefaultAdmin(ctx); err != nil {
		logger.Error("初始化默认 admin 用户失败", "error", err)
		return fmt.Errorf("初始化默认 admin 用户失败: %w", err)
	}

	// 创建 REST API Handler 与路由器
	handler := rest.NewHandler(kg, bus, registry, logger)
	// 注入数据库连接，使 healthz 能探测后端连通性（T6.3）
	handler.SetDB(db)
	// 注入认证依赖（根据配置决定是否启用路由保护）
	handler.SetAuth(authSvc, tm, cfg.Auth.Enabled)
	// 注入机器接入方服务（启用客户端管理 API 与 API Key 鉴权）
	handler.SetClientService(clientSvc)

	// 用量统计初始化（T1.8 + T3）
	// 1) 执行 schema 迁移（内置迁移器，幂等），确保 usage 等表存在
	migrator := storage.NewMigrator(db,
		storage.WithMigrations(storage.DefaultMigrations()),
		storage.WithRebind(dbRebind),
		storage.WithLogger(logger),
	)
	if err := migrator.Migrate(ctx); err != nil {
		logger.Error("执行 schema 迁移失败", "error", err)
		return fmt.Errorf("执行 schema 迁移失败: %w", err)
	}
	// 2) 创建 usage 存储后端（按存储类型选择实现）
	var usageStore usage.Store
	if cfg.Data.Type == "postgres" {
		usageStore = usage.NewPostgresStore(db)
	} else {
		usageStore = usage.NewSQLStore(db)
	}
	// 3) 创建 OTel 用量 metrics（T3.3）与异步聚合 Collector（T3.4）
	usageMetrics, err := usage.NewMetrics()
	if err != nil {
		logger.Warn("创建用量 metrics 失败，使用 nil（仅采集不入库）", "error", err)
		usageMetrics = nil
	}
	usageCollector := usage.NewCollector(usageStore, usage.CollectorConfig{}, logger)
	defer func() {
		if cerr := usageCollector.Close(); cerr != nil {
			logger.Error("关闭用量 Collector 失败", "error", cerr)
		}
	}()
	// 4) 注入用量查询存储到 Handler，启用 /billing/usage 端点
	handler.SetUsageStore(usageStore)
	// 5) 注入用量计量（为遥测上报的帧数/字节数计量 T5.6）
	handler.SetUsageMeter(usageCollector, usageMetrics)

	router := rest.NewRouter(handler)

	// 添加 OTel HTTP 中间件，为每个 HTTP 请求创建 span 并注入 traceID
	httpMiddleware := observability.HTTPMiddleware(cfg.Otel.ServiceName)
	router = httpMiddleware(router)

	// 添加用量计量中间件（T3.2）：解析主体、累计调用并埋点
	usageMiddleware := usage.HTTPMiddleware(usageCollector, usageMetrics)
	router = usageMiddleware(router)

	if cfg.Auth.Enabled {
		logger.Info("认证已启用")
	} else {
		logger.Warn("认证未启用（auth.enabled=false），所有路由可匿名访问")
	}

	// 初始化插件框架：创建 Broker（插件受限网关）与 Manager（插件管理器）
	broker := plugin.NewBroker(bus, kg, logger)
	pluginMgr := plugin.NewManager(broker, logger)

	// 从配置目录加载插件清单（MVP 阶段仅解析清单元数据）
	if err := pluginMgr.LoadFromDirectory(cfg.Plugin.Directory); err != nil {
		logger.Warn("从目录加载插件清单失败", "dir", cfg.Plugin.Directory, "error", err)
	}

	// 加载通过 init() 自动注册的全局内置插件
	pluginMgr.LoadGlobals()

	// 启动所有插件
	pluginMgr.StartAll()

	// 注入插件管理器到 REST Handler，启用插件管理 API
	handler.SetPluginManager(pluginMgr)

	// 注入插件管理器到 KGService，使 Node 生命周期钩子能够触发
	kg.SetHookTrigger(pluginMgr)

	// 注册内置遥测解析器（json、tle）到插件管理器
	pluginMgr.RegisterParser("json", &telemetry.JSONParser{})
	pluginMgr.RegisterParser("tle", &telemetry.TLEParser{})

	// 创建遥测流水线，设置默认解析器为 json
	telemetryPipeline := telemetry.NewPipeline(pluginMgr, bus, logger)
	if err := telemetryPipeline.SetParser("json"); err != nil {
		logger.Warn("设置默认遥测解析器失败", "error", err)
	}

	// 注入 Pipeline 到 REST Handler，启用遥测管理 API
	handler.SetTelemetryPipeline(telemetryPipeline)

	// 启动遥测流水线，监听 TCP 端口
	if err := telemetryPipeline.Start(ctx, cfg.Server.TelemetryPort); err != nil {
		logger.Error("启动遥测流水线失败", "error", err)
		return fmt.Errorf("启动遥测流水线失败: %w", err)
	}

	// 创建指令流水线
	commandPipeline := command.NewPipeline(pluginMgr, bus, logger)

	// 注册内置 Mock 指令适配器到插件管理器
	pluginMgr.RegisterAdapter("mock", command.NewMockAdapter(100*time.Millisecond, 1.0))

	// 设置默认指令适配器为 mock
	if err := commandPipeline.SetAdapter("mock"); err != nil {
		logger.Warn("设置默认指令适配器失败", "error", err)
	}

	// 启动指令流水线
	commandPipeline.Start()

	// 注入指令流水线到 REST Handler，启用指令管理 API
	handler.SetCommandPipeline(commandPipeline)

	// 创建 gRPC 服务器并启动，监听 cfg.Server.GRPCPort
	grpcServer := grpc.NewServer(kg, bus, registry, logger)
	// 注入 gRPC 鉴权依赖（与 REST 同源：JWT + 机器接入方 API Key）
	if tm != nil {
		grpcServer.SetAuth(tm, clientSvc)
	}
	// 注入遥测流水线与用量计量到 gRPC 服务，启用 IngestTelemetry（T5.2/5.6）
	grpcServer.SetTelemetryIngress(telemetryPipeline, usageCollector, usageMetrics)
	if err := grpcServer.Start(cfg.Server.GRPCPort); err != nil {
		logger.Error("启动 gRPC 服务失败", "error", err)
		return fmt.Errorf("启动 gRPC 服务失败: %w", err)
	}

	// 创建 HTTP 服务器
	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE 长连接需要禁用写超时
		IdleTimeout:  60 * time.Second,
	}

	// 启动 HTTP 服务（非阻塞）
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务监听中", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	// 捕获 SIGINT/SIGTERM 信号，优雅关闭
	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("HTTP 服务异常退出", "error", err)
			return err
		}
	case <-sigCtx.Done():
		logger.Info("收到终止信号，开始优雅关闭...")
	}

	// 给予最多 10 秒完成在途请求
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("优雅关闭失败", "error", err)
	}

	// 停止 gRPC 服务
	if err := grpcServer.Stop(); err != nil {
		logger.Error("停止 gRPC 服务失败", "error", err)
	}

	// 停止遥测流水线
	if err := telemetryPipeline.Stop(); err != nil {
		logger.Error("停止遥测流水线失败", "error", err)
	}

	// 停止指令流水线
	commandPipeline.Stop()

	// 停止所有插件
	pluginMgr.StopAll()

	// 关闭用户存储（当前为空操作，db 连接由外层 defer 关闭）
	if err := userStore.Close(); err != nil {
		logger.Error("关闭用户存储失败", "error", err)
	}

	// 关闭事件总线（会同时关闭 EventStore）
	if err := bus.Close(); err != nil {
		logger.Error("关闭事件总线失败", "error", err)
	}

	logger.Info("服务已停止")
	return nil
}

// parseLogLevel 将字符串日志级别转换为 slog.Level。
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
