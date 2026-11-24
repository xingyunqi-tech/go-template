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
# renovate: datasource=github-releases depName=sqlc-dev/sqlc
SQLC_VERSION := 1.31.1

ACTIONLINT_IMAGE := rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667
ZIZMOR_IMAGE := ghcr.io/zizmorcore/zizmor:1.32.0@sha256:3014cc8f68f663935f69b3c3e0e41f5ce102b268d97db13db0f62208747f860d
HADOLINT_IMAGE := hadolint/hadolint:v2.15.1@sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f
TRIVY_IMAGE := aquasec/trivy:0.76.2@sha256:8f6bb947dbc528902e11f2f5be52f7b519f6a39d5d148f41166f2040499768fb

GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
PREK := $(BIN)/prek-$(PREK_VERSION)
TYPOS := $(BIN)/typos-$(TYPOS_VERSION)
SQLC := $(BIN)/sqlc-$(SQLC_VERSION)
OPENAPIGEN := $(BIN)/openapigen

PKGS := ./...
IMAGE := go-template:local
BUILD_PLATFORMS := linux/amd64 linux/arm64
MOCK_SOURCES := internal/repo/contracts.go
MOCK_DIRS := $(sort $(foreach src,$(MOCK_SOURCES),$(dir $(src))mocks))
GENERATED := openapi/openapi.json internal/controller/http/validation.json internal/controller/http/gen internal/repo/postgres/sqlcgen $(MOCK_DIRS)
GITLEAKS_LOG_OPTS ?=
MIGRATIONS_SCHEMA := go_template_migrations
NAMING_SQL = SELECT con.conname FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = '$(1)' AND con.contype <> 'n' AND c.relname NOT LIKE 'river%' AND c.relname NOT LIKE 'goose%' UNION ALL SELECT indexname FROM pg_indexes WHERE schemaname = '$(1)' AND tablename NOT LIKE 'river%' AND tablename NOT LIKE 'goose%'
CONSTRAINT_NAME := ^[a-z0-9_]+_(pkey|key|fkey|idx|check)$$
TEST_FLAGS := -race -shuffle=on
COVERAGE_DIR := $(CURDIR)/coverage
DEV_COMPOSE := docker compose -f docker/docker-compose.base.yaml -f docker/docker-compose.postgres.yaml -f docker/docker-compose.redis.yaml -f docker/docker-compose.dev.yaml
E2E_COMPOSE := docker compose -p go-template-e2e -f docker/docker-compose.base.yaml -f docker/docker-compose.postgres.yaml -f docker/docker-compose.redis.yaml -f docker/docker-compose.ci.yaml

##@ 帮助

.PHONY: help
help: ## 列出可用目标
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ 开发

.PHONY: install
install: tools ## 安装工具二进制与 Git 钩子
	$(PREK) install

.PHONY: tools
tools: $(GOLANGCI_LINT) $(PREK) $(TYPOS) $(SQLC) ## 下载 golangci-lint、prek、typos、sqlc 官方二进制到 bin/
	$(GO) -C tools mod download

INFRA_COMPOSE := docker compose -f docker/infra.compose.yaml
ENV_RUN := set -a; [[ ! -f .env ]] || . ./.env; set +a;
PSQL := $(INFRA_COMPOSE) exec -T postgres psql -v ON_ERROR_STOP=1 -qAt -U "$${DATABASE_USERNAME:-root}"

.PHONY: infra
infra: ## 启动本地 Postgres 与 Redis（DATABASE_PORT / REDIS_PORT，默认 5432 / 6379）
	$(INFRA_COMPOSE) up -d --wait

.PHONY: up
up: ## 以 compose 启动开发栈（api、worker、Postgres、Redis、Mailpit），改代码后自动重建
	$(DEV_COMPOSE) up --build --watch

.PHONY: down
down: ## 停止开发栈
	$(DEV_COMPOSE) down

.PHONY: migrate
migrate: ## 创建 DATABASE_SCHEMA 并执行迁移（读取 .env）
	$(ENV_RUN) $(GO) run ./cmd/app migrate

.PHONY: migrate-create
migrate-create: ## 新建顺序编号的 goose SQL 迁移（NAME=add_orders，SET=core 或 identity，默认 core）
	@[[ -n "$(NAME)" ]] || { echo "用法：make migrate-create NAME=<name> [SET=core|identity]" >&2; exit 1; }
	$(GOTOOL) goose -dir migrations/$(or $(SET),core) -s create $(NAME) sql

.PHONY: seed
seed: ## 写入种子数据（读取 .env）
	$(ENV_RUN) $(GO) run ./cmd/app seed

.PHONY: keys
keys: ## 生成新的 Ed25519 签名密钥（AUTH_SIGNING_KEY）及其公钥（AUTH_VERIFY_KEYS）
	$(GO) run ./cmd/app keys generate

.PHONY: dev
dev: ## 本地启动 API（读取 .env；端口 APP_PORT，默认 3000）
	$(ENV_RUN) $(GO) run ./cmd/app serve

.PHONY: worker
worker: ## 本地启动队列 worker 与周期任务（读取 .env）
	$(ENV_RUN) $(GO) run ./cmd/app worker

.PHONY: generate
generate: openapi $(SQLC) ## 生成代码：OpenAPI bundle 与 oapi-codegen、sqlc、mockgen（MOCK_SOURCES 中的端口）
	$(SQLC) generate
	for dir in $(MOCK_DIRS); do rm -f "$$dir"/mock_*.go; done
	for src in $(MOCK_SOURCES); do \
		$(GOTOOL) mockgen -typed -source=$$src -package=mocks -destination=$$(dirname $$src)/mocks/mock_$$(basename $$src); \
	done

.PHONY: openapi
openapi: $(OPENAPIGEN) ## 由 openapi/src 生成 openapi/openapi.json、校验文档与 oapi-codegen 代码
	$(OPENAPIGEN)

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
vet: ## go vet + go build（含 integration / e2e / contract 标签）
	$(GO) vet -tags integration,e2e,contract $(PKGS)
	$(GO) -C tools vet ./...
	$(GO) build $(PKGS)

.PHONY: module
module: ## 以 note 为模板生成 CRUD 模块：make module NAME=<name> [PLURAL=<plural>] [PERMS=1]
	@test -n "$(NAME)" || { echo "用法：make module NAME=<name> [PLURAL=<plural>] [PERMS=1]" >&2; exit 1; }
	$(GO) run ./cmd/modulegen -name $(NAME) $(if $(PLURAL),-plural $(PLURAL)) $(if $(filter 1,$(PERMS)),-perms)
	$(MAKE) fmt

.PHONY: generate-check
generate-check: generate ## 重新生成后检查生成代码与入库版本一致
	@if [[ -n "$$(git status --porcelain -- $(GENERATED))" ]]; then \
		echo "生成代码与输入不一致，执行 make generate 并提交：" >&2; \
		git status --short -- $(GENERATED) >&2; \
		exit 1; \
	fi

.PHONY: hadolint
hadolint: ## hadolint 检查 Dockerfile（需要 Docker）
	docker run --rm -i $(HADOLINT_IMAGE) < Dockerfile

.PHONY: workflow-lint
workflow-lint: ## actionlint、zizmor（门槛 medium；设置 GH_TOKEN 时启用在线审计；需要 Docker）
	docker run --rm -v "$(CURDIR):/repo" -w /repo $(ACTIONLINT_IMAGE) -color
	docker run --rm -v "$(CURDIR):/repo:ro" -w /repo -e GH_TOKEN $(ZIZMOR_IMAGE) --min-severity medium .

.PHONY: check
check: lint vet test ## lint + vet + 单元测试

.PHONY: vuln
vuln: ## govulncheck
	$(GOTOOL) govulncheck $(PKGS)

.PHONY: gitleaks
gitleaks: ## gitleaks 扫描 Git 历史（GITLEAKS_LOG_OPTS 限定提交范围，如 origin/main..HEAD；需要 Docker）
	docker run --rm -v "$(CURDIR):/repo" $(GITLEAKS_IMAGE) git /repo --no-banner --redact $(if $(GITLEAKS_LOG_OPTS),--log-opts="$(GITLEAKS_LOG_OPTS)")

.PHONY: security
security: vuln gitleaks ## govulncheck + gitleaks

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
test-contract: $(OPENAPIGEN) ## 第 4 层：契约测试（文档与路由、policy 一致，bundle 无漂移）
	@mkdir -p $(COVERAGE_DIR)
	$(GOTOOL) gotestsum --junitfile $(COVERAGE_DIR)/contract.xml -- $(TEST_FLAGS) -tags contract -coverprofile=$(COVERAGE_DIR)/contract.out -coverpkg=./internal/...,./pkg/... ./tests/contract/...

.PHONY: test-migrations
test-migrations: $(SQLC) ## 迁移：在独立 schema 中 up → sqlc vet → down → 只剩 goose 版本表 → up，检查约束与索引命名（需要 make infra，读取 .env）
	$(ENV_RUN) \
	export DATABASE_SCHEMA=$(MIGRATIONS_SCHEMA) OTEL_SERVICE_NAME=$${OTEL_SERVICE_NAME:-go-template} OTEL_SDK_DISABLED=true; \
	$(PSQL) -d "$${DATABASE_NAME:-api}" -c 'DROP SCHEMA IF EXISTS $(MIGRATIONS_SCHEMA) CASCADE'; \
	$(GO) run ./cmd/app migrate; \
	$(SQLC) vet; \
	$(GO) run ./cmd/app migrate down; \
	left="$$($(PSQL) -d "$${DATABASE_NAME:-api}" -c "SELECT tablename FROM pg_tables WHERE schemaname = '$(MIGRATIONS_SCHEMA)' AND tablename NOT IN ('goose_core_version', 'goose_identity_version')")"; \
	if [[ -n "$$left" ]]; then echo "migrate down 后仍有表：$$left" >&2; exit 1; fi; \
	$(GO) run ./cmd/app migrate; \
	names="$$($(PSQL) -d "$${DATABASE_NAME:-api}" -c "$(call NAMING_SQL,$(MIGRATIONS_SCHEMA))")"; \
	bad="$$(grep -Ev '$(CONSTRAINT_NAME)' <<<"$$names" || true)"; \
	if [[ -n "$$bad" ]]; then echo "约束 / 索引命名应为 <table>_pkey、<table>_<columns>_key|fkey|idx|check：" >&2; echo "$$bad" >&2; exit 1; fi; \
	$(PSQL) -d "$${DATABASE_NAME:-api}" -c 'DROP SCHEMA $(MIGRATIONS_SCHEMA) CASCADE'

.PHONY: test-e2e
test-e2e: ## 第 3 层：构建镜像并以 compose 启动后黑盒测试（APP_URL，默认 http://localhost:3000）
	$(E2E_COMPOSE) up -d --build --wait || { $(E2E_COMPOSE) logs; $(E2E_COMPOSE) down -v; exit 1; }
	$(GOTOOL) gotestsum -- -count=1 -tags e2e ./tests/e2e/... || { $(E2E_COMPOSE) logs; $(E2E_COMPOSE) down -v; exit 1; }
	$(E2E_COMPOSE) down -v

.PHONY: coverage
coverage: ## 合并单元、集成与契约覆盖率并校验门槛（.testcoverage.yml）
	$(GOTOOL) go-test-coverage --config .testcoverage.yml

##@ 构建

.PHONY: build
build: ## 交叉编译全部子命令（linux amd64 / arm64）到 bin/<os>_<arch>/
	for platform in $(BUILD_PLATFORMS); do \
		CGO_ENABLED=0 GOOS=$${platform%/*} GOARCH=$${platform#*/} $(GO) build -trimpath -o $(BIN)/$${platform/\//_}/ ./cmd/...; \
	done

.PHONY: image
image: ## 构建生产镜像
	docker buildx build --load --build-arg VERSION=$$(git describe --always --dirty) -t $(IMAGE) .

.PHONY: image-scan
image-scan: image ## Trivy 扫描生产镜像（HIGH / CRITICAL，仅报告有修复版本的漏洞）
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v go-template-trivy-cache:/root/.cache/trivy $(TRIVY_IMAGE) \
		image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed --no-progress $(IMAGE)

.PHONY: ci
ci: lint hadolint workflow-lint vet generate-check test test-integration test-contract coverage infra test-migrations security build image-scan test-e2e ## 本地运行 check.yml 中的全部 job

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

$(SQLC): | $(BIN)
	$(CURL) https://github.com/sqlc-dev/sqlc/releases/download/v$(SQLC_VERSION)/sqlc_$(SQLC_VERSION)_$(OS)_$(ARCH).tar.gz \
		| tar -xzO sqlc > $@
	chmod +x $@

$(OPENAPIGEN): tools/go.mod tools/go.sum $(wildcard tools/openapigen/*.go) | $(BIN)
	$(GO) build -C tools -buildvcs=false -o $@ ./openapigen
