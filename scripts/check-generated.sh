#!/usr/bin/env bash
# pre-commit 钩子：生成输入（openapi/、tools/openapigen/）有暂存改动时重新生成并检查生成代码无 diff。
set -euo pipefail

make --no-print-directory generate >/dev/null
if ! git diff --exit-code --quiet -- internal/controller/http/gen; then
  echo "生成代码与输入不一致，已重新生成，请检查并暂存：" >&2
  git diff --stat -- internal/controller/http/gen >&2
  exit 1
fi
