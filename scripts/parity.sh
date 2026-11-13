#!/usr/bin/env bash
# 第 5 层 parity：启动 nestjs 参照服务与本服务，运行 tests/parity。
#
# 变量：
#   NESTJS_DIR               nestjs-template 本地仓库（默认 ../nestjs-template）
#   NESTJS_PORT              nestjs 映射到宿主的端口（默认 3001）
#   PARITY_PORT              本服务端口（默认 3000）
#   PARITY_NESTJS_URL        已在运行的 nestjs 地址；设置后不启动参照服务
#   PARITY_THROTTLE_ENABLED  两侧的 THROTTLE_ENABLED（默认 false）
#   PARITY_KEEP              设为 1 时测试结束后保留 nestjs 参照服务；再次运行时按本次配置更新
#   PARITY_APP_NAME          本服务的 APP_NAME（默认取 nestjs env-example-relational 中的值）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NESTJS_DIR="${NESTJS_DIR:-$ROOT/../nestjs-template}"
export NESTJS_PORT="${NESTJS_PORT:-3001}"
PARITY_PORT="${PARITY_PORT:-3000}"
THROTTLE="${PARITY_THROTTLE_ENABLED:-false}"
PROJECT="go-template-parity-nestjs"
COMPOSE=(docker compose -p "$PROJECT" -f "$ROOT/scripts/nestjs/nestjs.compose.yaml")
STAGE="$(mktemp -d)"
APP_PID=""

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
  if [[ -n "$APP_PID" ]]; then
    kill "$APP_PID" 2>/dev/null || true
    wait "$APP_PID" 2>/dev/null || true
  fi
  if [[ -z "${PARITY_NESTJS_URL:-}" && "${PARITY_KEEP:-0}" != 1 ]]; then
    "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$STAGE"
}
trap cleanup EXIT

if port_busy "$PARITY_PORT"; then
  echo "端口 ${PARITY_PORT} 已被占用，请设置 PARITY_PORT 为空闲端口后重试" >&2
  exit 1
fi

ref="$(sed -n 's/^commit=//p' "$ROOT/openapi/SOURCE")"
ref="$(git -C "$NESTJS_DIR" rev-parse --verify "${ref}^{commit}")"
APP_NAME="${PARITY_APP_NAME:-$(git -C "$NESTJS_DIR" show "${ref}:env-example-relational" | sed -n 's/^APP_NAME=//p' | tr -d '"')}"

if [[ -z "${PARITY_NESTJS_URL:-}" ]]; then
  export NESTJS_IMAGE="fastapi-temp/nestjs-ref:${ref:0:12}"
  export NESTJS_ENV_FILE="$STAGE/env"

  if ! docker image inspect "$NESTJS_IMAGE" >/dev/null 2>&1; then
    worktree="$STAGE/nestjs"
    git -C "$NESTJS_DIR" worktree add --detach "$worktree" "$ref" >/dev/null
    docker build --target production -t "$NESTJS_IMAGE" "$worktree"
    git -C "$NESTJS_DIR" worktree remove --force "$worktree"
  fi

  {
    git -C "$NESTJS_DIR" show "${ref}:env-example-relational"
    echo "THROTTLE_ENABLED=${THROTTLE}"
    echo "TRUST_PROXY=1"
  } >"$NESTJS_ENV_FILE"

  if ! "${COMPOSE[@]}" ps --status running --services 2>/dev/null | grep -qx api && port_busy "$NESTJS_PORT"; then
    echo "端口 ${NESTJS_PORT} 已被占用，请设置 NESTJS_PORT 为空闲端口后重试" >&2
    exit 1
  fi
  "${COMPOSE[@]}" up -d --wait postgres redis
  "${COMPOSE[@]}" up -d api
  PARITY_NESTJS_URL="http://localhost:${NESTJS_PORT}"
fi

go build -o "$STAGE/app" "$ROOT/cmd/app"
APP_ENV=development APP_NAME="$APP_NAME" APP_PORT="$PARITY_PORT" LOG_LEVEL=warn TRUST_PROXY=1 THROTTLE_ENABLED="$THROTTLE" \
  "$STAGE/app" serve >"$STAGE/app.log" 2>&1 &
APP_PID=$!

wait_for "${PARITY_NESTJS_URL}/"
wait_for "http://localhost:${PARITY_PORT}/" || { cat "$STAGE/app.log" >&2; exit 1; }

PARITY_GO_URL="http://localhost:${PARITY_PORT}" PARITY_NESTJS_URL="$PARITY_NESTJS_URL" \
  go tool -modfile="$ROOT/tools/go.mod" gotestsum -- -count=1 -tags parity "$ROOT/tests/parity/..."
