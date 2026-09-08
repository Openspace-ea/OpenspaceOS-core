package grpc

import (
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/core"
	"github.com/openspace-os/openspace-os-core/internal/pipeline/telemetry"
	"github.com/openspace-os/openspace-os-core/internal/usage"
	"github.com/openspace-os/openspace-os-core/pkg/event"
)

// Server 是 Openspace OS Core 的 gRPC 服务器，封装 gRPC Server 的创建、启动与停止。
type Server struct {
	kg       *core.KGService
	bus      core.MessageBus
	registry *event.SchemaRegistry
	logger   *slog.Logger
	listener net.Listener
	server   *grpc.Server

	// 鉴权依赖：TokenManager 与机器接入方 ClientService。
	// 二者同时提供时启用 gRPC 鉴权 interceptor；否则不鉴权（开发/演示）。
	tm        *auth.TokenManager
	clientSvc *auth.ClientService

	// 遥测 ingest 依赖（T5.2/5.6）：流水线与用量计量。
	pipeline        *telemetry.Pipeline
	usageCollector  *usage.Collector
	usageMetrics    *usage.Metrics
}

// NewServer 创建 gRPC 服务器实例。
//
// 传入领域服务、事件总线与 Schema 注册中心依赖。
// 调用 Start 方法后才会实际监听端口。
func NewServer(kg *core.KGService, bus core.MessageBus, registry *event.SchemaRegistry, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		kg:       kg,
		bus:      bus,
		registry: registry,
		logger:   logger,
	}
}

// SetAuth 注入 gRPC 鉴权依赖（JWT 与机器接入方 API Key）。
//
// tm 不能为 nil；clientSvc 可为 nil（此时仅支持 JWT）。调用 Start 前设置。
func (s *Server) SetAuth(tm *auth.TokenManager, clientSvc *auth.ClientService) {
	s.tm = tm
	s.clientSvc = clientSvc
}

// SetTelemetryIngress 注入遥测流水线与用量计量，启用 IngestTelemetry（T5.2/5.6）。
//
// 在 main.go 创建遥测 Pipeline 与 usage 计量后调用，应在 Start 之前设置。
func (s *Server) SetTelemetryIngress(p *telemetry.Pipeline, collector *usage.Collector, metrics *usage.Metrics) {
	s.pipeline = p
	s.usageCollector = collector
	s.usageMetrics = metrics
}

// Start 启动 gRPC 服务，监听指定端口。
//
// 此方法为非阻塞：创建 TCP 监听器后，在后台 goroutine 中调用 Serve。
// 如果端口绑定失败则立即返回错误。
func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("gRPC 监听失败 (port=%d): %w", port, err)
	}
	s.listener = lis

	options := []grpc.ServerOption{}
	if s.tm != nil {
		interceptor := NewAuthInterceptor(s.tm, s.clientSvc, s.logger)
		options = append(options,
			grpc.ChainUnaryInterceptor(interceptor.Unary()),
			grpc.ChainStreamInterceptor(interceptor.Stream()),
		)
	}
	s.server = grpc.NewServer(options...)
	srv := newKGServer(s.kg, s.bus, s.registry, s.logger)
	srv.setTelemetryIngress(s.pipeline, s.usageCollector, s.usageMetrics)
	RegisterKGServiceServer(s.server, srv)

	go func() {
		s.logger.Info("gRPC 服务监听中", "addr", addr)
		if err := s.server.Serve(lis); err != nil {
			s.logger.Error("gRPC 服务异常退出", "error", err)
		}
	}()

	return nil
}

// Stop 优雅停止 gRPC 服务。
//
// 调用 GracefulStop 等待所有在途 RPC 完成后关闭服务器。
func (s *Server) Stop() error {
	if s.server != nil {
		s.server.GracefulStop()
		s.logger.Info("gRPC 服务已停止")
	}
	return nil
}
