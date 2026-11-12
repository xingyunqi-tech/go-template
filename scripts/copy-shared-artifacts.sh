#!/usr/bin/env bash
# 从 fastapi-temp 复制共享产物（openapi/、db/、scripts/ 中的生成工具、校验用例）。
# 用法：scripts/copy-shared-artifacts.sh [FASTAPI_DIR]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${1:-$ROOT/../fastapi-temp}"
FILES=(
  openapi/nestjs-baseline.json
  openapi/validation.json
  openapi/SOURCE
  openapi/nest_handlers.json
  db/nestjs-schema.sql
  scripts/openapi-sync.sh
  scripts/schema_snapshot.py
  scripts/gen_validation_cases.py
  scripts/gen_nest_handlers.py
  scripts/nestjs/validation-cases.ts
  scripts/nestjs/nestjs.compose.yaml
  tests/contract/fixtures/validation-cases.json
)

if [[ ! -f "$SRC/openapi/SOURCE" ]]; then
  echo "未找到 $SRC/openapi/SOURCE，设置 FASTAPI_DIR 指向 fastapi-temp" >&2
  exit 1
fi

if [[ -n "$(git -C "$SRC" status --porcelain -- "${FILES[@]}")" ]]; then
  echo "$SRC 中的共享产物有未提交改动，先在 fastapi-temp 提交" >&2
  exit 1
fi

for f in "${FILES[@]}"; do
  mkdir -p "$ROOT/$(dirname "$f")"
  cp -p "$SRC/$f" "$ROOT/$f"
done

echo "==> 已从 ${SRC}（$(git -C "$SRC" rev-parse --short HEAD)）复制共享产物"
git -C "$ROOT" status --short -- "${FILES[@]}"
