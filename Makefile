# Openspace OS Core Makefile
# 同时兼容 Windows（GNU Make for Windows）与 Linux
# 注意：Windows 下需安装 GNU Make（如通过 chocolatey: choco install make）

# Go 相关变量
GO        := go
GOLANGCI  := golangci-lint
PROTOC    := protoc

# 构建输出目录
BIN_DIR   := bin

# 依据平台决定可执行文件后缀（Windows 需要 .exe）
ifeq ($(OS),Windows_NT)
	EXE_EXT := .exe
else
	EXE_EXT :=
endif

CORE_BIN := $(BIN_DIR)/openspace-os-core$(EXE_EXT)
CLI_BIN  := $(BIN_DIR)/openspace-os-cli$(EXE_EXT)

# Docker 相关变量
DOCKER      := docker
COMPOSE     := docker-compose
COMPOSE_FILE := deployments/docker-compose.yml
DOCKERFILE  := deployments/Dockerfile
IMAGE_NAME  := openspace-os-core:latest

# 默认目标
.PHONY: all
all: build

##@ 构建

# 编译 Core 服务和 CLI 到 bin/ 目录
.PHONY: build
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(CORE_BIN) ./cmd/openspace-os-core
	$(GO) build -o $(CLI_BIN) ./cmd/openspace-os-cli

##@ 运行

# 启动 Core 服务
.PHONY: run
run:
	$(GO) run ./cmd/openspace-os-core run --config config.yaml

##@ 测试与检查

# 运行所有测试
.PHONY: test
test:
	$(GO) test ./...

# 运行 golangci-lint（如已安装）
.PHONY: lint
lint:
	@command -v $(GOLANGCI) >/dev/null 2>&1 && $(GOLANGCI) run ./... || echo "golangci-lint 未安装，跳过 lint"

##@ 依赖管理

# 运行 go mod tidy
.PHONY: tidy
tidy:
	$(GO) mod tidy

##@ 代码生成

# 生成 gRPC 代码（需要 protoc / protoc-gen-go / protoc-gen-go-grpc）
.PHONY: proto
proto:
	@command -v $(PROTOC) >/dev/null 2>&1 && \
		$(PROTOC) --go_out=. --go_opt=paths=source_relative \
			--go-grpc_out=. --go-grpc_opt=paths=source_relative \
			internal/api/grpc/proto/openspace_os_core.proto || echo "protoc 未安装，跳过 proto 生成"

##@ Docker

# 构建 Docker 镜像
.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(IMAGE_NAME) -f $(DOCKERFILE) .

# 通过 docker-compose 启动服务（后台运行）
.PHONY: docker-run
docker-run:
	$(COMPOSE) -f $(COMPOSE_FILE) up -d --build

# 停止并移除 Docker 容器
.PHONY: docker-stop
docker-stop:
	$(COMPOSE) -f $(COMPOSE_FILE) down

# 查看容器日志
.PHONY: docker-logs
docker-logs:
	$(COMPOSE) -f $(COMPOSE_FILE) logs -f

# 运行部署冒烟测试（需服务已启动，依赖 curl 与 jq）
.PHONY: smoke-test
smoke-test:
	@command -v curl >/dev/null 2>&1 || { echo "curl 未安装，无法运行冒烟测试"; exit 1; }
	@command -v jq >/dev/null 2>&1 || { echo "jq 未安装，无法运行冒烟测试"; exit 1; }
	bash deployments/smoke_test.sh

##@ 清理

# 清理 bin/ 目录
.PHONY: clean
clean:
	@rm -rf $(BIN_DIR)

##@ 帮助

# 显示帮助信息
.PHONY: help
help:
	@echo "Openspace OS Core Makefile"
	@echo ""
	@echo "可用目标:"
	@echo "  build        - 编译 Core 服务和 CLI 到 bin/ 目录"
	@echo "  run          - 启动 Core 服务"
	@echo "  test         - 运行所有测试"
	@echo "  lint         - 运行 golangci-lint（如已安装）"
	@echo "  tidy         - 运行 go mod tidy"
	@echo "  proto        - 生成 gRPC 代码（需要 protoc）"
	@echo "  docker-build - 构建 Docker 镜像"
	@echo "  docker-run   - 通过 docker-compose 启动服务"
	@echo "  docker-stop  - 停止并移除 Docker 容器"
	@echo "  docker-logs  - 查看容器日志"
	@echo "  smoke-test   - 运行部署冒烟测试"
	@echo "  clean        - 清理 bin/ 目录"
