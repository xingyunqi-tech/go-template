#!/usr/bin/env bash
# 第 5 层 parity：启动 nestjs 参照服务与本服务（compose：migrate、seed、api，Postgres、Redis），运行 tests/parity。
#
# 变量：
#   NESTJS_DIR               nestjs-template 本地仓库（默认 ../nestjs-template）
#   NESTJS_PORT              nestjs 映射到宿主的端口（默认 3001）
#   PARITY_PORT              本服务映射到宿主的端口（默认 3000）
#   PARITY_NESTJS_URL        已在运行的 nestjs 地址；设置后不启动参照服务
#   PARITY_THROTTLE_ENABLED  两侧的 THROTTLE_ENABLED（默认 true）
#   PARITY_KEEP              设为 1 时测试结束后保留两侧服务；再次运行时按本次配置更新
#   PARITY_APP_NAME          本服务的 APP_NAME（默认取 nestjs env-example-relational 中的值）
#   PARITY_NESTJS_SPEC       设为 0 时跳过 nestjs test/user/auth.e2e-spec.ts（默认指向本服务运行，运行前本服务改为限流关闭）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NESTJS_DIR="${NESTJS_DIR:-$ROOT/../nestjs-template}"
export NESTJS_PORT="${NESTJS_PORT:-3001}"
export PARITY_PORT="${PARITY_PORT:-3000}"
export PARITY_THROTTLE_ENABLED="${PARITY_THROTTLE_ENABLED:-true}"
NESTJS=(docker compose -p go-template-parity-nestjs -f "$ROOT/scripts/nestjs/nestjs.compose.yaml")
SERVICE=(docker compose -p go-template-parity
  -f "$ROOT/docker/docker-compose.base.yaml"
  -f "$ROOT/docker/docker-compose.postgres.yaml"
  -f "$ROOT/docker/docker-compose.redis.yaml"
  -f "$ROOT/docker/docker-compose.parity.yaml")
STAGE="$(mktemp -d)"
OWN_NESTJS=false

port_busy() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

wait_for() {
  local url="$1"
  for _ in $(seq 1 90); do
    if curl -sf --max-time 5 -o /dev/null "$url"; then
      return 0
    fi
    sleep 2
  done
  echo "$url 未能在 180s 内就绪" >&2
  return 1
}

cleanup() {
  if [[ "${PARITY_KEEP:-0}" != 1 ]]; then
    "${SERVICE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    if [[ "$OWN_NESTJS" == true ]]; then
      "${NESTJS[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
    fi
  fi
  rm -rf "$STAGE"
}
trap cleanup EXIT

ref="$(sed -n 's/^commit=//p' "$ROOT/openapi/SOURCE")"
ref="$(git -C "$NESTJS_DIR" rev-parse --verify "${ref}^{commit}")"
export PARITY_APP_NAME="${PARITY_APP_NAME:-$(git -C "$NESTJS_DIR" show "${ref}:env-example-relational" | sed -n 's/^APP_NAME=//p' | tr -d '"')}"
export NESTJS_IMAGE="fastapi-temp/nestjs-ref:${ref:0:12}"
export NESTJS_ENV_FILE="$STAGE/env"
{
  git -C "$NESTJS_DIR" show "${ref}:env-example-relational"
  echo "THROTTLE_ENABLED=${PARITY_THROTTLE_ENABLED}"
  echo "TRUST_PROXY=1"
} >"$NESTJS_ENV_FILE"

if ! "${SERVICE[@]}" ps --status running --services 2>/dev/null | grep -qx api && port_busy "$PARITY_PORT"; then
  echo "端口 ${PARITY_PORT} 已被占用，请设置 PARITY_PORT 为空闲端口后重试" >&2
  exit 1
fi

if [[ -z "${PARITY_NESTJS_URL:-}" ]]; then
  OWN_NESTJS=true
  if ! docker image inspect "$NESTJS_IMAGE" >/dev/null 2>&1; then
    worktree="$STAGE/nestjs"
    git -C "$NESTJS_DIR" worktree add --detach "$worktree" "$ref" >/dev/null
    docker build --target production -t "$NESTJS_IMAGE" "$worktree"
    git -C "$NESTJS_DIR" worktree remove --force "$worktree"
  fi

  if ! "${NESTJS[@]}" ps --status running --services 2>/dev/null | grep -qx api && port_busy "$NESTJS_PORT"; then
    echo "端口 ${NESTJS_PORT} 已被占用，请设置 NESTJS_PORT 为空闲端口后重试" >&2
    exit 1
  fi
  "${NESTJS[@]}" up -d --wait postgres redis
  "${NESTJS[@]}" up -d api
  PARITY_NESTJS_URL="http://localhost:${NESTJS_PORT}"
fi

"${SERVICE[@]}" up -d --build --wait || { "${SERVICE[@]}" logs >&2; exit 1; }

wait_for "${PARITY_NESTJS_URL}/"
wait_for "http://localhost:${PARITY_PORT}/" || { "${SERVICE[@]}" logs api >&2; exit 1; }

PARITY_GO_URL="http://localhost:${PARITY_PORT}" PARITY_NESTJS_URL="$PARITY_NESTJS_URL" \
  go tool -modfile="$ROOT/tools/go.mod" gotestsum -- -count=1 -tags parity "$ROOT/tests/parity/..."

if [[ "${PARITY_NESTJS_SPEC:-1}" == 1 ]]; then
  # nestjs 的 auth spec 按其 CI 配置在限流关闭时运行
  PARITY_THROTTLE_ENABLED=false "${SERVICE[@]}" up -d --wait api
  docker run --rm --network host -e APP_PORT="$PARITY_PORT" "${NESTJS_IMAGE}-builder" \
    pnpm exec vitest run --config vitest.e2e.config.mts test/user/auth.e2e-spec.ts
fi
