.DEFAULT_GOAL := help
SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

GO ?= go
GOTOOL := $(GO) tool -modfile=tools/go.mod
BIN := $(CURDIR)/bin
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH := $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
RUST_ARCH := $(shell uname -m | sed -e 's/arm64/aarch64/')
RUST_OS := $(if $(filter darwin,$(OS)),apple-darwin,unknown-linux-musl)
CURL := curl -fsSL --retry 5 --retry-all-errors --retry-delay 3

# renovate: datasource=github-releases depName=golangci/golangci-lint
GOLANGCI_LINT_VERSION := 2.15.0
# renovate: datasource=github-releases depName=j178/prek
PREK_VERSION := 0.5.10
# renovate: datasource=github-releases depName=crate-ci/typos
TYPOS_VERSION := 1.52.2

GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
PREK := $(BIN)/prek-$(PREK_VERSION)
TYPOS := $(BIN)/typos-$(TYPOS_VERSION)
OPENAPIGEN := $(BIN)/openapigen

PKGS := ./...
TEST_FLAGS := -race -shuffle=on
COVERAGE_DIR := $(CURDIR)/coverage
FASTAPI_DIR ?= $(CURDIR)/../fastapi-temp
E2E_COMPOSE := docker compose -p go-template-e2e -f docker/docker-compose.base.yaml -f docker/docker-compose.ci.yaml

##@ 帮助

.PHONY: help
help: ## 列出可用目标
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ 开发

.PHONY: install
install: tools ## 安装工具二进制与 Git 钩子
	$(PREK) install

.PHONY: tools
tools: $(GOLANGCI_LINT) $(PREK) $(TYPOS) ## 下载 golangci-lint、prek、typos 官方二进制到 bin/
	$(GO) -C tools mod download

.PHONY: dev
dev: ## 本地启动 API（读取 .env；端口 APP_PORT，默认 3000）
	set -a; [[ ! -f .env ]] || . ./.env; set +a; $(GO) run ./cmd/app serve

.PHONY: generate
generate: $(OPENAPIGEN) ## 生成代码：oapi-codegen（scope.yaml 范围内）
	$(OPENAPIGEN)

.PHONY: openapi-sync
openapi-sync: ## 从 fastapi-temp 复制共享产物（FASTAPI_DIR）；OPENAPI_SYNC=run 时在 nestjs 上重新导出（需 Docker 与 uv）
ifeq ($(OPENAPI_SYNC),run)
	scripts/openapi-sync.sh
else
	scripts/copy-shared-artifacts.sh "$(FASTAPI_DIR)"
endif

##@ 检查

.PHONY: fmt
fmt: $(GOLANGCI_LINT) ## 格式化（gofumpt + gci）
	$(GOLANGCI_LINT) fmt

.PHONY: lint
lint: $(GOLANGCI_LINT) $(TYPOS) ## golangci-lint（主模块与 tools 模块）、typos、go mod tidy -diff
	$(GOLANGCI_LINT) run
	cd tools && $(GOLANGCI_LINT) run --config ../.golangci.yml ./...
	$(TYPOS)
	$(GO) mod tidy -diff
	$(GO) -C tools mod tidy -diff

.PHONY: lint-new
lint-new: $(GOLANGCI_LINT) ## golangci-lint，只报告相对 HEAD 新增的问题（pre-commit 钩子）
	$(GOLANGCI_LINT) run --new-from-rev=HEAD

.PHONY: typos
typos: $(TYPOS) ## 拼写检查
	$(TYPOS)

.PHONY: vet
vet: ## go vet + go build（含 integration / e2e / contract / parity 标签）
	$(GO) vet -tags integration,e2e,contract,parity $(PKGS)
	$(GO) -C tools vet ./...
	$(GO) build $(PKGS)

.PHONY: check
check: lint vet test ## lint + vet + 单元测试

.PHONY: vuln
vuln: ## govulncheck
	$(GOTOOL) govulncheck $(PKGS)

##@ 测试

.PHONY: test
test: ## 第 1 层：单元测试
	@mkdir -p $(COVERAGE_DIR)
	$(GOTOOL) gotestsum --junitfile $(COVERAGE_DIR)/unit.xml -- $(TEST_FLAGS) -coverprofile=$(COVERAGE_DIR)/unit.out -coverpkg=./internal/...,./pkg/... $(PKGS)

.PHONY: test-integration
test-integration: ## 第 2 层：集成测试（testcontainers，需要 Docker）
	@mkdir -p $(COVERAGE_DIR)
	$(GOTOOL) gotestsum --junitfile $(COVERAGE_DIR)/integration.xml -- $(TEST_FLAGS) -tags integration -coverprofile=$(COVERAGE_DIR)/integration.out -coverpkg=./internal/...,./pkg/... $(PKGS)

.PHONY: test-contract
test-contract: ## 第 4 层：契约测试
	$(GOTOOL) gotestsum -- $(TEST_FLAGS) -tags contract ./tests/contract/...

.PHONY: test-e2e
test-e2e: ## 第 3 层：构建镜像并以 compose 启动后黑盒测试（APP_URL，默认 http://localhost:3000）
	$(E2E_COMPOSE) up -d --build --wait
	$(GOTOOL) gotestsum -- -count=1 -tags e2e ./tests/e2e/... || { $(E2E_COMPOSE) logs; $(E2E_COMPOSE) down -v; exit 1; }
	$(E2E_COMPOSE) down -v

.PHONY: coverage
coverage: ## 合并单元与集成覆盖率并校验门槛（.testcoverage.yml）
	$(GOTOOL) go-test-coverage --config .testcoverage.yml

.PHONY: image
image: ## 构建生产镜像
	docker buildx build --load --build-arg VERSION=$$(git describe --always --dirty) -t go-template:local .

.PHONY: ci
ci: check test-integration test-contract coverage test-e2e image ## 本地运行 CI 门禁

.PHONY: clean
clean: ## 清理构建与测试产物
	rm -rf $(BIN) $(COVERAGE_DIR)

##@ 工具二进制

$(BIN):
	@mkdir -p $(BIN)

$(GOLANGCI_LINT): | $(BIN)
	$(CURL) https://github.com/golangci/golangci-lint/releases/download/v$(GOLANGCI_LINT_VERSION)/golangci-lint-$(GOLANGCI_LINT_VERSION)-$(OS)-$(ARCH).tar.gz \
		| tar -xzO golangci-lint-$(GOLANGCI_LINT_VERSION)-$(OS)-$(ARCH)/golangci-lint > $@
	chmod +x $@

$(PREK): | $(BIN)
	$(CURL) https://github.com/j178/prek/releases/download/v$(PREK_VERSION)/prek-$(RUST_ARCH)-$(RUST_OS).tar.gz \
		| tar -xzO prek-$(RUST_ARCH)-$(RUST_OS)/prek > $@
	chmod +x $@

$(TYPOS): | $(BIN)
	$(CURL) https://github.com/crate-ci/typos/releases/download/v$(TYPOS_VERSION)/typos-v$(TYPOS_VERSION)-$(RUST_ARCH)-$(RUST_OS).tar.gz \
		| tar -xzO ./typos > $@
	chmod +x $@

$(OPENAPIGEN): tools/go.mod tools/go.sum $(wildcard tools/openapigen/*.go) | $(BIN)
	$(GO) build -C tools -o $@ ./openapigen
