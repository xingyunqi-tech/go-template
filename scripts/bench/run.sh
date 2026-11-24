#!/usr/bin/env bash
# 用法：scripts/bench/run.sh up|bench|down
#
# up 构建生产镜像并以 compose 起 api（API_CPUS、API_MEM 限额）、Postgres、Redis 与种子账号；bench 对 ENDPOINTS 的每个接口
# 在 VU_LEVELS 的每个并发下跑 DURATION，k6 摘要写入 OUT_DIR；down 删除栈与数据卷。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PROJECT=go-template-bench
K6_IMAGE="${K6_IMAGE:-grafana/k6:1.4.2}"
VU_LEVELS="${VU_LEVELS:-10 50}"
ENDPOINTS="${ENDPOINTS:-me notes create-note refresh login}"
DURATION="${DURATION:-30s}"
API_CPUS="${API_CPUS:-2}"
API_MEM="${API_MEM:-1g}"
OUT_DIR="${OUT_DIR:-${TMPDIR:-/tmp}/go-bench}"

compose() {
  docker compose -p "$PROJECT" \
    -f "$ROOT/docker/docker-compose.base.yaml" -f "$ROOT/docker/docker-compose.postgres.yaml" \
    -f "$ROOT/docker/docker-compose.redis.yaml" -f "$ROOT/scripts/bench/compose.yaml" "$@"
}

case "${1:-}" in
  up)
    API_CPUS="$API_CPUS" API_MEM="$API_MEM" compose up -d --build --wait
    ;;
  bench)
    mkdir -p "$OUT_DIR"
    network="${PROJECT}_default"
    for endpoint in $ENDPOINTS; do
      for vus in $VU_LEVELS; do
        echo "== $endpoint, $vus VUs, $DURATION"
        docker run --rm --network "$network" -v "$ROOT/scripts/bench:/bench:ro" -v "$OUT_DIR:/out" \
          -e ENDPOINT="$endpoint" -e VUS="$vus" -e DURATION="$DURATION" "$K6_IMAGE" \
          run --quiet --summary-export "/out/$endpoint-$vus.json" /bench/bench.js
      done
    done
    echo "summaries in $OUT_DIR"
    ;;
  down)
    compose down -v
    ;;
  *)
    sed -n '2,5p' "$0" >&2
    exit 2
    ;;
esac
